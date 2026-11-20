# AWS S3 runs

These runs use real AWS S3 (`TARGET=s3`). They are not comparable with
`results-oci.md` (OCI Object Storage) or `results-floci.md` (local mock).

```bash
TARGET=s3 BUCKET=<empty bucket> REGION=<region> ./benchmarks/floci-bench/run.sh
TARGET=s3 BUCKET=<empty bucket> REGION=<region> LAYOUT=flat ./benchmarks/floci-bench/run.sh
```

Defaults: `OBJECTS=300000`, `OBJ_SIZE=1024`, `LAYOUT=nested`, `PREFIXES=256`,
`CONCURRENCY=25`, `RUNS=3`. Credentials come from env, a profile or IAM, unless
`ACCESS_KEY` is set. The bucket must be empty and is cleaned up afterwards.

## Summary

| Version | Layout | min | median | max | entries/s (median) |
|---|---|---|---|---|---|
| 2.6.0 | nested | 7.691s | 8.192s | 8.267s | ~41,500 |
| 2.6.0 | flat | 1m47.52s | 1m54.179s | 1m54.778s | ~2,980 |
| 2.5.0 | nested | 1m26.995s | 1m29.271s | 1m33.961s | ~3,360 |
| 2.5.0 | flat | 1m31.306s | 1m32.31s | 1m35.856s | ~3,250 |

The two versions ran on different data, so compare their throughput rather
than their durations:

- 2.6.0 lists 340,000 entries: 290,000 current objects, 40,000 noncurrent versions and 10,000 delete markers.
- 2.5.0 lists 300,000 objects with no versions.

## 2.6.0

Sharded listing along `/` prefixes. Data: 290,000 current objects, 40,000
noncurrent versions and 10,000 delete markers, 340,000 entries in all.

| Layout | min | median | max | entries/s (median) |
|---|---|---|---|---|
| nested | 7.691s | 8.192s | 8.267s | ~41,500 |
| flat | 1m47.52s | 1m54.179s | 1m54.778s | ~2,980 |

### nested

```text
Measuring S3UsageInfo (concurrency=25, runs=3)...
  run 1: objects=290000  versions=40000  deleteMarkers=10000  size=322.27 MiB  duration=8.192s  (41503 entries/s)
  run 2: objects=290000  versions=40000  deleteMarkers=10000  size=322.27 MiB  duration=7.691s  (44206 entries/s)
  run 3: objects=290000  versions=40000  deleteMarkers=10000  size=322.27 MiB  duration=8.267s  (41127 entries/s)

min=7.691s  median=8.192s  max=8.267s
```

### flat

```text
Measuring S3UsageInfo (concurrency=25, runs=3)...
  run 1: objects=290000  versions=40000  deleteMarkers=10000  size=322.27 MiB  duration=1m47.52s  (3162 entries/s)
  run 2: objects=290000  versions=40000  deleteMarkers=10000  size=322.27 MiB  duration=1m54.179s  (2978 entries/s)
  run 3: objects=290000  versions=40000  deleteMarkers=10000  size=322.27 MiB  duration=1m54.778s  (2962 entries/s)

min=1m47.52s  median=1m54.179s  max=1m54.778s
```

## 2.5.0

Sequential listing, one range per bucket. Data: 300,000 objects, with no
versions and no delete markers.

| Layout | min | median | max | objects/s (median) |
|---|---|---|---|---|
| nested | 1m26.995s | 1m29.271s | 1m33.961s | ~3,360 |
| flat | 1m31.306s | 1m32.31s | 1m35.856s | ~3,250 |

### nested

```text
Measuring S3UsageInfo (concurrency=25, runs=3)...
  run 1: objects=300000  size=292.97 MiB  deleteMarkers=0  duration=1m29.271s  (3361 obj/s)
  run 2: objects=300000  size=292.97 MiB  deleteMarkers=0  duration=1m26.995s  (3448 obj/s)
  run 3: objects=300000  size=292.97 MiB  deleteMarkers=0  duration=1m33.961s  (3193 obj/s)

min=1m26.995s  median=1m29.271s  max=1m33.961s
```

### flat

```text
Measuring S3UsageInfo (concurrency=25, runs=3)...
  run 1: objects=300000  size=292.97 MiB  deleteMarkers=0  duration=1m31.306s  (3286 obj/s)
  run 2: objects=300000  size=292.97 MiB  deleteMarkers=0  duration=1m35.856s  (3130 obj/s)
  run 3: objects=300000  size=292.97 MiB  deleteMarkers=0  duration=1m32.31s  (3250 obj/s)

min=1m31.306s  median=1m32.31s  max=1m35.856s
```
