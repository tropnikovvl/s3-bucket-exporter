# OCI Object Storage runs

Real OCI Object Storage via its Amazon S3 Compatibility API (`TARGET=s3`),
run from a laptop over the internet. Not comparable with
`results-s3.md` (real AWS S3) or `results-floci.md` (local mock).

## Versioning auto-detect: ListObjectsV2 vs ListObjectVersions

Date: 2026-11-20. Same data in both buckets, so the runs compare the two
listing APIs directly:

- `s3-exp-wo` is a bucket that never had versioning enabled. `GetBucketVersioning` returns an empty `<VersioningConfiguration/>`, so the exporter lists it with `ListObjectsV2`.
- `s3-exp-ver` is a bucket with versioning enabled. `GetBucketVersioning` returns `<Status>Enabled</Status>`, so the exporter lists it with `ListObjectVersions`.

```bash
TARGET=s3 ENDPOINT=https://<namespace>.compat.objectstorage.<region>.oraclecloud.com \
REGION=<region> ACCESS_KEY=<customer secret key id> SECRET_KEY=<secret> \
VERSIONS=0 DELETE_MARKERS=0 BUCKET=<bucket> [LAYOUT=flat] ./benchmarks/floci-bench/run.sh
```

Defaults: `OBJECTS=300000`, `OBJ_SIZE=1024`, `LAYOUT=nested`, `PREFIXES=256`,
`CONCURRENCY=25`, `RUNS=3`. Each bucket was empty before its run.

`VERSIONS` and `DELETE_MARKERS` stay at 0 because otherwise the bench calls
`PutBucketVersioning`, which OCI's S3 API does not document. The versioned
bucket therefore holds no noncurrent versions, only current ones.

| Layout | Bucket | Listing API | min | median | max | entries/s (median) |
|---|---|---|---|---|---|---|
| nested | `s3-exp-wo` | ListObjectsV2 | 20.096s | 20.93s | 21.444s | ~14,300 |
| nested | `s3-exp-ver` | ListObjectVersions | 25.656s | 25.778s | 26.405s | ~11,600 |
| flat | `s3-exp-wo` | ListObjectsV2 | 20.133s | 20.285s | 20.442s | ~14,800 |
| flat | `s3-exp-ver` | ListObjectVersions | 26.884s | 27.166s | 27.772s | ~11,000 |

ListObjectsV2 is faster on both layouts:

- nested: about 19% less time (1.23x the throughput);
- flat: about 25% less time (1.34x the throughput).

Seeding took 4m39s–4m47s for every run, so the network and the backend were
comparable across runs.

Flat, which lists as one sequential range, is about as fast as nested, which
lists as 25 parallel ranges. Parallel listing therefore gains nothing here: the
limit is the link or OCI's request rate, not the exporter's concurrency.

### nested: s3-exp-wo (ListObjectsV2)

```text
Seed complete: 300000 objects + 0 versions + 0 delete markers in 4m39.778s
Measuring S3UsageInfo (concurrency=25, runs=3)...
  run 1: objects=300000  versions=0  deleteMarkers=0  size=292.97 MiB  duration=20.93s  (14333 entries/s)
  run 2: objects=300000  versions=0  deleteMarkers=0  size=292.97 MiB  duration=21.444s  (13990 entries/s)
  run 3: objects=300000  versions=0  deleteMarkers=0  size=292.97 MiB  duration=20.096s  (14928 entries/s)

min=20.096s  median=20.93s  max=21.444s
```

### nested: s3-exp-ver (ListObjectVersions)

```text
Seed complete: 300000 objects + 0 versions + 0 delete markers in 4m39.323s
Measuring S3UsageInfo (concurrency=25, runs=3)...
  run 1: objects=300000  versions=0  deleteMarkers=0  size=292.97 MiB  duration=26.405s  (11362 entries/s)
  run 2: objects=300000  versions=0  deleteMarkers=0  size=292.97 MiB  duration=25.656s  (11693 entries/s)
  run 3: objects=300000  versions=0  deleteMarkers=0  size=292.97 MiB  duration=25.778s  (11638 entries/s)

min=25.656s  median=25.778s  max=26.405s
```

### flat: s3-exp-wo (ListObjectsV2)

```text
Seed complete: 300000 objects + 0 versions + 0 delete markers in 4m41.29s
Measuring S3UsageInfo (concurrency=25, runs=3)...
  run 1: objects=300000  versions=0  deleteMarkers=0  size=292.97 MiB  duration=20.285s  (14789 entries/s)
  run 2: objects=300000  versions=0  deleteMarkers=0  size=292.97 MiB  duration=20.133s  (14901 entries/s)
  run 3: objects=300000  versions=0  deleteMarkers=0  size=292.97 MiB  duration=20.442s  (14676 entries/s)

min=20.133s  median=20.285s  max=20.442s
Cleaned up 300000 objects
```

### flat: s3-exp-ver (ListObjectVersions)

```text
Seed complete: 300000 objects + 0 versions + 0 delete markers in 4m47.091s
Measuring S3UsageInfo (concurrency=25, runs=3)...
  run 1: objects=300000  versions=0  deleteMarkers=0  size=292.97 MiB  duration=27.772s  (10802 entries/s)
  run 2: objects=300000  versions=0  deleteMarkers=0  size=292.97 MiB  duration=26.884s  (11159 entries/s)
  run 3: objects=300000  versions=0  deleteMarkers=0  size=292.97 MiB  duration=27.166s  (11043 entries/s)

min=26.884s  median=27.166s  max=27.772s
Cleaned up 300000 objects
```

### Notes for running against OCI

- Credentials are a Customer Secret Key (access key = key id). A new key took several minutes to be accepted everywhere: until then requests alternated between success and `SignatureDoesNotMatch`.
- OCI's docs list neither `GetBucketVersioning` nor `ListObjectVersions` nor `ListObjectsV2`, but all three work on the S3 endpoint.
- OCI rate-limits bulk deletes per key:
  - Under parallel `DeleteObjects` it answers HTTP 200 but marks individual keys `429 TooManyRequests: rate-limit exceeded`, and the SDK does not retry those.
  - The bench cleanup now re-sends failed keys with backoff and fails loudly if any remain.
  - Before that fix, the nested cleanup reported "Cleaned up 300000" yet left about 205k objects behind. A first flat run measured those leftovers and was discarded. The flat numbers above come from a rerun on empty buckets.
