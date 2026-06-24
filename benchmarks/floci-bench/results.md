# How to run

```bash
./benchmarks/floci-bench/run.sh
```

# 2.5.0

Floci

```text
Measuring S3UsageInfo (concurrency=25, runs=3)...
  run 1: objects=300000  size=292.97 MiB  deleteMarkers=0  duration=2m45.535s  (1812 obj/s)
  run 2: objects=300000  size=292.97 MiB  deleteMarkers=0  duration=2m38.474s  (1893 obj/s)
  run 3: objects=300000  size=292.97 MiB  deleteMarkers=0  duration=2m31.674s  (1978 obj/s)

min=2m31.674s  median=2m38.474s  max=2m45.535s
```

S3

```text
Measuring S3UsageInfo (concurrency=25, runs=3)...
  run 1: objects=300000  size=292.97 MiB  deleteMarkers=0  duration=1m29.271s  (3361 obj/s)
  run 2: objects=300000  size=292.97 MiB  deleteMarkers=0  duration=1m26.995s  (3448 obj/s)
  run 3: objects=300000  size=292.97 MiB  deleteMarkers=0  duration=1m33.961s  (3193 obj/s)

min=1m26.995s  median=1m29.271s  max=1m33.961s
```
