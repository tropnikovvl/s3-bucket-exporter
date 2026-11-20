# Local floci A/B runs

These numbers come from the local floci mock (`TARGET=floci`), not real S3.
They are only good for before/after comparison on the same machine. Do not
compare them with `results-s3.md`, which holds real AWS S3 runs.

## Versioning auto-detect (ListObjectsV2 for never-versioned buckets)

Date: 2026-11-20. Base: `87b279a` (before) vs the same tree with versioning
auto-detect (after).

Bucket created without versioning:

```bash
VERSIONS=0 DELETE_MARKERS=0 LAYOUT=flat   ./benchmarks/floci-bench/run.sh
VERSIONS=0 DELETE_MARKERS=0 LAYOUT=nested ./benchmarks/floci-bench/run.sh
```

Defaults: `OBJECTS=300000`, `OBJ_SIZE=1024`, `PREFIXES=256`, `CONCURRENCY=25`, `RUNS=3`.

| Layout | Before (ListObjectVersions) median | After (ListObjectsV2) median | Change |
|---|---|---|---|
| nested | 3m4.506s | 39.352s | ~4.7x faster |
| flat | 1m1.875s | 58.166s | ~6% faster |

### Before

S3 nested

```text
Measuring S3UsageInfo (concurrency=25, runs=3)...
  run 1: objects=300000  versions=0  deleteMarkers=0  size=292.97 MiB  duration=3m9.708s  (1581 entries/s)
  run 2: objects=300000  versions=0  deleteMarkers=0  size=292.97 MiB  duration=3m4.506s  (1626 entries/s)
  run 3: objects=300000  versions=0  deleteMarkers=0  size=292.97 MiB  duration=3m3.775s  (1632 entries/s)

min=3m3.775s  median=3m4.506s  max=3m9.708s
```

S3 flat

```text
Measuring S3UsageInfo (concurrency=25, runs=3)...
  run 1: objects=300000  versions=0  deleteMarkers=0  size=292.97 MiB  duration=1m1.961s  (4842 entries/s)
  run 2: objects=300000  versions=0  deleteMarkers=0  size=292.97 MiB  duration=1m1.875s  (4848 entries/s)
  run 3: objects=300000  versions=0  deleteMarkers=0  size=292.97 MiB  duration=1m0.713s  (4941 entries/s)

min=1m0.713s  median=1m1.875s  max=1m1.961s
```

### After

S3 nested

```text
Measuring S3UsageInfo (concurrency=25, runs=3)...
  run 1: objects=300000  versions=0  deleteMarkers=0  size=292.97 MiB  duration=39.352s  (7623 entries/s)
  run 2: objects=300000  versions=0  deleteMarkers=0  size=292.97 MiB  duration=39.509s  (7593 entries/s)
  run 3: objects=300000  versions=0  deleteMarkers=0  size=292.97 MiB  duration=34.224s  (8766 entries/s)

min=34.224s  median=39.352s  max=39.509s
```

S3 flat

```text
Measuring S3UsageInfo (concurrency=25, runs=3)...
  run 1: objects=300000  versions=0  deleteMarkers=0  size=292.97 MiB  duration=57.079s  (5256 entries/s)
  run 2: objects=300000  versions=0  deleteMarkers=0  size=292.97 MiB  duration=1m3.806s  (4702 entries/s)
  run 3: objects=300000  versions=0  deleteMarkers=0  size=292.97 MiB  duration=58.166s  (5158 entries/s)

min=57.079s  median=58.166s  max=1m3.806s
```

Flat gains little because, with no `/` prefixes to split on, the bucket is
still listed by one sequential range (see the adaptive sharding spec).
