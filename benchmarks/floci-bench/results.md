# How to run

```bash
./benchmarks/floci-bench/run.sh
```

# 2.6.0

S3 nested

```text
Measuring S3UsageInfo (concurrency=25, runs=3)...
  run 1: objects=290000  versions=40000  deleteMarkers=10000  size=322.27 MiB  duration=8.192s  (41503 entries/s)
  run 2: objects=290000  versions=40000  deleteMarkers=10000  size=322.27 MiB  duration=7.691s  (44206 entries/s)
  run 3: objects=290000  versions=40000  deleteMarkers=10000  size=322.27 MiB  duration=8.267s  (41127 entries/s)

min=7.691s  median=8.192s  max=8.267s
```

S3 flat

```text
Measuring S3UsageInfo (concurrency=25, runs=3)...
  run 1: objects=290000  versions=40000  deleteMarkers=10000  size=322.27 MiB  duration=1m47.52s  (3162 entries/s)
  run 2: objects=290000  versions=40000  deleteMarkers=10000  size=322.27 MiB  duration=1m54.179s  (2978 entries/s)
  run 3: objects=290000  versions=40000  deleteMarkers=10000  size=322.27 MiB  duration=1m54.778s  (2962 entries/s)

min=1m47.52s  median=1m54.179s  max=1m54.778s
```

# 2.5.0

S3 nested

```text
Measuring S3UsageInfo (concurrency=25, runs=3)...
  run 1: objects=300000  size=292.97 MiB  deleteMarkers=0  duration=1m29.271s  (3361 obj/s)
  run 2: objects=300000  size=292.97 MiB  deleteMarkers=0  duration=1m26.995s  (3448 obj/s)
  run 3: objects=300000  size=292.97 MiB  deleteMarkers=0  duration=1m33.961s  (3193 obj/s)

min=1m26.995s  median=1m29.271s  max=1m33.961s
```

S3 flat

```text
Measuring S3UsageInfo (concurrency=25, runs=3)...
  run 1: objects=300000  size=292.97 MiB  deleteMarkers=0  duration=1m31.306s  (3286 obj/s)
  run 2: objects=300000  size=292.97 MiB  deleteMarkers=0  duration=1m35.856s  (3130 obj/s)
  run 3: objects=300000  size=292.97 MiB  deleteMarkers=0  duration=1m32.31s  (3250 obj/s)

min=1m31.306s  median=1m32.31s  max=1m35.856s
```
