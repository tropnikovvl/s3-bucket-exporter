// Command floci-bench benchmarks the exporter's bucket-listing path against a
// floci (S3-compatible) endpoint. It can seed a bucket with many small objects
// and then time controllers.S3UsageInfo — the exact code the exporter runs — so
// the same measurement is comparable across code versions (e.g. master vs a
// sharded-listing branch).
//
// Against floci (local mock), use run.sh which manages the container.
//
// Against a real (manually created, empty) bucket — seed, measure, and delete
// all objects afterwards (also on Ctrl-C):
//
//	go run ./benchmarks/floci-bench -bucket my-empty-bucket -region us-east-1 \
//	    -seed -cleanup -objects 300000 -obj-size 1024 -layout nested -runs 3
//
// Credentials come from the default chain (env / profile / IAM) when
// -access-key is empty; pass -access-key/-secret-key for static keys.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/tropnikovvl/s3-bucket-exporter/internal/auth"
	"github.com/tropnikovvl/s3-bucket-exporter/internal/controllers"
	"golang.org/x/sync/errgroup"
)

const maxObjSize = 5 * 1024 // objects must not exceed 5 KB

func main() { os.Exit(run()) }

func run() int {
	endpoint := flag.String("endpoint", "", "S3 endpoint URL (empty = real AWS; floci: http://localhost:4566)")
	region := flag.String("region", "us-east-1", "S3 region")
	accessKey := flag.String("access-key", "", "S3 access key (empty = IAM / default credential chain)")
	secretKey := flag.String("secret-key", "", "S3 secret key")
	bucket := flag.String("bucket", "bench", "Bucket name (must already exist)")
	pathStyle := flag.Bool("path-style", true, "Path-style addressing (auto: false when -endpoint is empty / real AWS)")

	seed := flag.Bool("seed", false, "Seed the bucket with objects before measuring")
	cleanup := flag.Bool("cleanup", false, "Delete all objects from the bucket on exit (and on interrupt)")
	objects := flag.Int("objects", 300000, "Number of objects to seed")
	objSize := flag.Int("obj-size", 1024, "Object payload size in bytes (capped at 5120)")
	layout := flag.String("layout", "nested", "Key layout: flat | nested")
	prefixes := flag.Int("prefixes", 256, "Top-level prefixes for the nested layout")
	versions := flag.Int("versions", 30000, "Number of objects given an extra (noncurrent) version (enables bucket versioning)")
	deleteMarkers := flag.Int("delete-markers", 10000, "Number of objects to delete-mark (enables bucket versioning)")
	seedWorkers := flag.Int("seed-workers", 64, "Concurrent PUTs while seeding")

	concurrency := flag.Int("concurrency", 25, "maxConcurrency passed to S3UsageInfo")
	runs := flag.Int("runs", 1, "How many times to measure the listing")
	flag.Parse()

	if *objSize > maxObjSize {
		fmt.Fprintf(os.Stderr, "error: -obj-size must not exceed %d bytes\n", maxObjSize)
		return 2
	}

	// Path-style defaults to false for real AWS (empty endpoint) unless set.
	usePathStyle := *pathStyle
	if *endpoint == "" && !flagSet("path-style") {
		usePathStyle = false
	}

	// Cancel in-flight work on Ctrl-C / SIGTERM; cleanup still runs afterwards.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	authCfg := auth.AuthConfig{
		Region:    *region,
		Endpoint:  *endpoint,
		AccessKey: *accessKey,
		SecretKey: *secretKey,
	}
	authCfg.Method = auth.DetectAuthMethod(authCfg)
	awsCfg, err := auth.NewAWSAuth(authCfg).GetConfig(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: failed to configure AWS auth: %v\n", err)
		return 1
	}
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.UsePathStyle = usePathStyle
	})

	dest := *endpoint
	if dest == "" {
		dest = "AWS (" + *region + ")"
	}
	fmt.Printf("Endpoint: %s   Bucket: %s   Region: %s   Auth: %s\n", dest, *bucket, *region, authCfg.Method)

	// Always clean up seeded data on the way out — normal exit or interrupt —
	// using a fresh context so it runs even after ctx was cancelled by a signal.
	if *cleanup {
		defer func() {
			cctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()
			fmt.Println(">> cleaning up bucket...")
			n, err := cleanBucket(cctx, client, *bucket, *seedWorkers)
			if err != nil {
				fmt.Fprintf(os.Stderr, "cleanup error (after deleting %d): %v\n", n, err)
				return
			}
			fmt.Printf("Cleaned up %d objects\n", n)
		}()
	}

	if *seed {
		if err := seedBucket(ctx, client, *bucket, *objects, *objSize, *layout, *prefixes, *seedWorkers, *versions, *deleteMarkers); err != nil {
			fmt.Fprintf(os.Stderr, "seed error: %v\n", err)
			return 1
		}
	}

	if err := measure(ctx, client, *bucket, *region, *concurrency, *runs); err != nil {
		fmt.Fprintf(os.Stderr, "measure error: %v\n", err)
		return 1
	}
	return 0
}

func flagSet(name string) bool {
	found := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

func keyFor(i, n, prefixes int, layout string) string {
	switch layout {
	case "nested":
		return fmt.Sprintf("p%05d/%09d", i%prefixes, i)
	default: // flat
		return fmt.Sprintf("obj-%09d", i)
	}
}

// selectEven picks exactly min(count, total) indices out of [0, total), spread
// evenly across the range.
func selectEven(i, total, count int) bool {
	if count <= 0 || total <= 0 {
		return false
	}
	return (i+1)*count/total > i*count/total
}

func seedBucket(ctx context.Context, client *s3.Client, bucket string, objects, objSize int, layout string, prefixes, workers, versions, deleteMarkers int) error {
	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)})
	if err != nil && !alreadyExists(err) {
		return fmt.Errorf("create bucket: %w", err)
	}

	if versions > 0 || deleteMarkers > 0 {
		if _, err := client.PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{
			Bucket:                  aws.String(bucket),
			VersioningConfiguration: &types.VersioningConfiguration{Status: types.BucketVersioningStatusEnabled},
		}); err != nil {
			return fmt.Errorf("enable versioning: %w", err)
		}
	}

	payload := make([]byte, objSize)
	fmt.Printf("Seeding %d objects (%d B each, layout=%s) + %d versions + %d delete markers, %d workers...\n",
		objects, objSize, layout, versions, deleteMarkers, workers)
	start := time.Now()

	// stepFor returns a logging interval that yields ~5 progress lines.
	stepFor := func(n int) int64 {
		if s := int64(n) / 5; s > 0 {
			return s
		}
		return 1
	}
	verStep := stepFor(versions)

	// Phase 1: PUT objects (selected keys get a second PUT → noncurrent version).
	fmt.Println(">> phase 1: putting objects (and versions)...")
	var done, extra int64
	g := new(errgroup.Group)
	g.SetLimit(workers)
	for i := 0; i < objects; i++ {
		g.Go(func() error {
			key := keyFor(i, objects, prefixes, layout)
			puts := 1
			if selectEven(i, objects, versions) {
				puts = 2
			}
			for p := 0; p < puts; p++ {
				if _, err := client.PutObject(ctx, &s3.PutObjectInput{
					Bucket: aws.String(bucket),
					Key:    aws.String(key),
					Body:   bytes.NewReader(payload),
				}); err != nil {
					return err
				}
			}
			if puts == 2 {
				if v := atomic.AddInt64(&extra, 1); v%verStep == 0 {
					fmt.Printf("  versions %d/%d (%s)\n", v, versions, time.Since(start).Round(time.Second))
				}
			}
			if n := atomic.AddInt64(&done, 1); n%10000 == 0 {
				fmt.Printf("  objects %d/%d (%s)\n", n, objects, time.Since(start).Round(time.Second))
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return fmt.Errorf("put object: %w", err)
	}

	// Phase 2: delete-mark selected keys (DeleteObject without VersionId on a
	// versioned bucket adds a delete marker over the current version).
	var marked int64
	if deleteMarkers > 0 {
		fmt.Println(">> phase 2: delete-marking keys...")
		dmStep := stepFor(deleteMarkers)
		dg := new(errgroup.Group)
		dg.SetLimit(workers)
		for i := 0; i < objects; i++ {
			if !selectEven(i, objects, deleteMarkers) {
				continue
			}
			key := keyFor(i, objects, prefixes, layout)
			dg.Go(func() error {
				if _, err := client.DeleteObject(ctx, &s3.DeleteObjectInput{
					Bucket: aws.String(bucket),
					Key:    aws.String(key),
				}); err != nil {
					return err
				}
				if m := atomic.AddInt64(&marked, 1); m%dmStep == 0 {
					fmt.Printf("  delete-markers %d/%d (%s)\n", m, deleteMarkers, time.Since(start).Round(time.Second))
				}
				return nil
			})
		}
		if err := dg.Wait(); err != nil {
			return fmt.Errorf("delete-mark: %w", err)
		}
	}

	fmt.Printf("Seed complete: %d objects + %d versions + %d delete markers in %s\n\n",
		objects, atomic.LoadInt64(&extra), atomic.LoadInt64(&marked), time.Since(start).Round(time.Millisecond))
	return nil
}

func alreadyExists(err error) bool {
	var owned *types.BucketAlreadyOwnedByYou
	var exists *types.BucketAlreadyExists
	return errors.As(err, &owned) || errors.As(err, &exists) ||
		strings.Contains(err.Error(), "BucketAlreadyOwnedByYou") ||
		strings.Contains(err.Error(), "BucketAlreadyExists")
}

func measure(ctx context.Context, client controllers.S3ClientInterface, bucket, region string, concurrency, runs int) error {
	fmt.Printf("Measuring S3UsageInfo (concurrency=%d, runs=%d)...\n", concurrency, runs)
	durations := make([]time.Duration, 0, runs)
	for r := 1; r <= runs; r++ {
		start := time.Now()
		summary, err := controllers.S3UsageInfo(ctx, region, client, bucket, concurrency)
		elapsed := time.Since(start)
		if err != nil {
			return fmt.Errorf("run %d: %w", r, err)
		}
		durations = append(durations, elapsed)

		var current, versions, size float64
		for _, m := range summary.StorageClasses {
			current += m.CurrentObjectNumber
			versions += m.NoncurrentObjectNumber
			size += m.CurrentSize + m.NoncurrentSize
		}
		entries := current + versions + summary.DeleteMarkers // total entries the listing walked
		rate := 0.0
		if elapsed > 0 {
			rate = entries / elapsed.Seconds()
		}
		fmt.Printf("  run %d: objects=%.0f  versions=%.0f  deleteMarkers=%.0f  size=%s  duration=%s  (%.0f entries/s)\n",
			r, current, versions, summary.DeleteMarkers, humanizeBytes(int64(size)), elapsed.Round(time.Millisecond), rate)
	}

	if runs > 1 {
		sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
		fmt.Printf("\nmin=%s  median=%s  max=%s\n",
			durations[0].Round(time.Millisecond),
			durations[len(durations)/2].Round(time.Millisecond),
			durations[len(durations)-1].Round(time.Millisecond))
	}
	return nil
}

// cleanBucket removes every version and delete marker from the bucket and
// returns how many were removed. It lists via ListObjectVersions and deletes by
// (Key, VersionId) so it fully empties a versioned bucket (a plain delete on a
// versioned bucket would only add delete markers). Listing is sequential
// (paginated); per-page DeleteObjects calls run concurrently (up to `workers`).
func cleanBucket(ctx context.Context, client *s3.Client, bucket string, workers int) (int, error) {
	p := s3.NewListObjectVersionsPaginator(client, &s3.ListObjectVersionsInput{Bucket: aws.String(bucket)})
	var total int64

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(workers)
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			_ = g.Wait()
			return int(atomic.LoadInt64(&total)), err
		}
		ids := make([]types.ObjectIdentifier, 0, len(page.Versions)+len(page.DeleteMarkers))
		for _, v := range page.Versions {
			ids = append(ids, types.ObjectIdentifier{Key: v.Key, VersionId: v.VersionId})
		}
		for _, dm := range page.DeleteMarkers {
			ids = append(ids, types.ObjectIdentifier{Key: dm.Key, VersionId: dm.VersionId})
		}
		if len(ids) == 0 {
			continue
		}
		g.Go(func() error {
			if _, err := client.DeleteObjects(gctx, &s3.DeleteObjectsInput{
				Bucket: aws.String(bucket),
				Delete: &types.Delete{Objects: ids, Quiet: aws.Bool(true)},
			}); err != nil {
				return err
			}
			atomic.AddInt64(&total, int64(len(ids)))
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return int(atomic.LoadInt64(&total)), err
	}
	return int(atomic.LoadInt64(&total)), nil
}

func humanizeBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}
