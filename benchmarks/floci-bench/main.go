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
			cctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			fmt.Println(">> cleaning up bucket...")
			n, err := cleanBucket(cctx, client, *bucket)
			if err != nil {
				fmt.Fprintf(os.Stderr, "cleanup error (after deleting %d): %v\n", n, err)
				return
			}
			fmt.Printf("Cleaned up %d objects\n", n)
		}()
	}

	if *seed {
		if err := seedBucket(ctx, client, *bucket, *objects, *objSize, *layout, *prefixes, *seedWorkers); err != nil {
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

func seedBucket(ctx context.Context, client *s3.Client, bucket string, objects, objSize int, layout string, prefixes, workers int) error {
	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)})
	if err != nil && !alreadyExists(err) {
		return fmt.Errorf("create bucket: %w", err)
	}

	payload := make([]byte, objSize)
	fmt.Printf("Seeding %d objects (%d B each, layout=%s) with %d workers...\n", objects, objSize, layout, workers)
	start := time.Now()

	var done int64
	g := new(errgroup.Group)
	g.SetLimit(workers)
	for i := 0; i < objects; i++ {
		g.Go(func() error {
			_, err := client.PutObject(ctx, &s3.PutObjectInput{
				Bucket: aws.String(bucket),
				Key:    aws.String(keyFor(i, objects, prefixes, layout)),
				Body:   bytes.NewReader(payload),
			})
			if err != nil {
				return err
			}
			if n := atomic.AddInt64(&done, 1); n%10000 == 0 {
				fmt.Printf("  seeded %d/%d (%s)\n", n, objects, time.Since(start).Round(time.Second))
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return fmt.Errorf("put object: %w", err)
	}
	fmt.Printf("Seed complete: %d objects in %s\n\n", objects, time.Since(start).Round(time.Millisecond))
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

		var objs, size float64
		for _, m := range summary.StorageClasses {
			objs += m.CurrentObjectNumber + m.NoncurrentObjectNumber
			size += m.CurrentSize + m.NoncurrentSize
		}
		rate := 0.0
		if elapsed > 0 {
			rate = objs / elapsed.Seconds()
		}
		fmt.Printf("  run %d: objects=%.0f  size=%s  deleteMarkers=%.0f  duration=%s  (%.0f obj/s)\n",
			r, objs, humanizeBytes(int64(size)), summary.DeleteMarkers, elapsed.Round(time.Millisecond), rate)
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

// cleanBucket deletes all objects in the bucket (page by page, up to 1000 per
// DeleteObjects call) and returns how many were removed.
func cleanBucket(ctx context.Context, client *s3.Client, bucket string) (int, error) {
	p := s3.NewListObjectsV2Paginator(client, &s3.ListObjectsV2Input{Bucket: aws.String(bucket)})
	total := 0
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return total, err
		}
		if len(page.Contents) == 0 {
			continue
		}
		ids := make([]types.ObjectIdentifier, 0, len(page.Contents))
		for _, o := range page.Contents {
			ids = append(ids, types.ObjectIdentifier{Key: o.Key})
		}
		if _, err := client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(bucket),
			Delete: &types.Delete{Objects: ids, Quiet: aws.Bool(true)},
		}); err != nil {
			return total, err
		}
		total += len(ids)
	}
	return total, nil
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
