// Command floci-bench benchmarks the exporter's bucket-listing path against a
// floci (S3-compatible) endpoint. It can seed a bucket with many small objects
// and then time controllers.S3UsageInfo — the exact code the exporter runs — so
// the same measurement is comparable across code versions (e.g. master vs a
// sharded-listing branch).
//
// Start floci first:
//
//	docker run --rm -p 4566:4566 -e FLOCI_HOSTNAME=floci hectorvent/floci:latest
//
// Seed once, then measure (repeatedly):
//
//	go run ./benchmarks/floci-bench -seed -objects 300000 -obj-size 1024 -layout nested
//	go run ./benchmarks/floci-bench -runs 3 -concurrency 25
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/tropnikovvl/s3-bucket-exporter/internal/auth"
	"github.com/tropnikovvl/s3-bucket-exporter/internal/controllers"
	"golang.org/x/sync/errgroup"
)

const maxObjSize = 5 * 1024 // objects must not exceed 5 KB

func main() {
	endpoint := flag.String("endpoint", "http://localhost:4566", "floci/S3 endpoint URL")
	region := flag.String("region", "us-east-1", "S3 region")
	accessKey := flag.String("access-key", "test", "S3 access key")
	secretKey := flag.String("secret-key", "test", "S3 secret key")
	bucket := flag.String("bucket", "bench", "Bucket name")

	seed := flag.Bool("seed", false, "Create the bucket and seed it with objects before measuring")
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
		os.Exit(2)
	}

	ctx := context.Background()

	authCfg := auth.AuthConfig{
		Region:    *region,
		Endpoint:  *endpoint,
		AccessKey: *accessKey,
		SecretKey: *secretKey,
	}
	authCfg.Method = auth.DetectAuthMethod(authCfg)
	awsCfg, err := auth.NewAWSAuth(authCfg).GetConfig(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: failed to configure AWS auth: %v\n", err)
		os.Exit(1)
	}
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.UsePathStyle = true // floci requires path-style addressing
	})

	fmt.Printf("Endpoint: %s   Bucket: %s   Region: %s\n", *endpoint, *bucket, *region)

	if *seed {
		if err := seedBucket(ctx, client, *bucket, *objects, *objSize, *layout, *prefixes, *seedWorkers); err != nil {
			fmt.Fprintf(os.Stderr, "seed error: %v\n", err)
			os.Exit(1)
		}
	}

	measure(ctx, client, *bucket, *region, *concurrency, *runs)
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

func measure(ctx context.Context, client controllers.S3ClientInterface, bucket, region string, concurrency, runs int) {
	fmt.Printf("Measuring S3UsageInfo (concurrency=%d, runs=%d)...\n", concurrency, runs)
	durations := make([]time.Duration, 0, runs)
	for r := 1; r <= runs; r++ {
		start := time.Now()
		summary, err := controllers.S3UsageInfo(ctx, region, client, bucket, concurrency)
		elapsed := time.Since(start)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  run %d error: %v\n", r, err)
			os.Exit(1)
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
