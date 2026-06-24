"""Verify exporter metrics against the actual S3 state, per storage class."""
import logging
import time

from e2elib.state import class_metrics

logger = logging.getLogger(__name__)


def _class_checks(prefix, actual_class, exporter_class):
    """Return a list of mismatch strings for one storage class."""
    oc = exporter_class.get("object_count", {})
    ts = exporter_class.get("total_size", {})
    pairs = [
        ("current object count", oc.get("current", 0), actual_class["current_count"]),
        ("current size", ts.get("current", 0), actual_class["current_size"]),
        ("noncurrent object count", oc.get("noncurrent", 0), actual_class["noncurrent_count"]),
        ("noncurrent size", ts.get("noncurrent", 0), actual_class["noncurrent_size"]),
    ]
    return [f"{prefix} {what} mismatch. Expected: {exp}, Got: {act}"
            for what, act, exp in pairs if act != exp]


def verify_metrics_match_state(actual_state: dict, exporter_metrics: dict, label: str = "check") -> None:
    """Assert exporter metrics equal the actual S3 state. Raises AssertionError on any mismatch."""
    logger.debug(f"--- {label}: Verifying metrics ---")

    assert exporter_metrics.get("endpoint_up") == 1, f"{label}: Endpoint should be up"

    # s3_bucket_count counts ALL buckets at the endpoint; this equality assumes
    # actual_state covers every bucket present (guaranteed by the isolated test
    # endpoint). Reusing against a shared endpoint would need a different count.
    expected_bucket_count = len(actual_state)
    actual_bucket_count = exporter_metrics.get("bucket_count", 0)
    assert actual_bucket_count == expected_bucket_count, (
        f"{label}: Bucket count mismatch. Expected: {expected_bucket_count}, Got: {actual_bucket_count}"
    )

    errors = []
    # Expected per-class totals, accumulated across buckets.
    expected_totals: dict = {}

    for bucket, bstate in actual_state.items():
        bucket_data = exporter_metrics.get(bucket, {})
        exporter_classes = bucket_data.get("storage_classes", {})
        actual_classes = bstate["storage_classes"]

        for sc in set(actual_classes) | set(exporter_classes):
            actual_class = actual_classes.get(sc, class_metrics())
            errors.extend(_class_checks(f"Bucket '{bucket}' [{sc}]",
                                        actual_class, exporter_classes.get(sc, {})))
            agg = expected_totals.setdefault(sc, class_metrics())
            for k in agg:
                agg[k] += actual_class[k]

        actual_dm = bucket_data.get("delete_markers", 0)
        if actual_dm != bstate["delete_markers"]:
            errors.append(
                f"Bucket '{bucket}' delete markers mismatch. "
                f"Expected: {bstate['delete_markers']}, Got: {actual_dm}"
            )

    if errors:
        # Do not log here: under verify_with_retry a failed attempt is an
        # expected transient (exporter hasn't scraped yet). The message rides
        # on the AssertionError, which surfaces only if retries are exhausted.
        raise AssertionError(f"{label} failed with {len(errors)} error(s):\n" + "\n".join(errors))

    # Per-class totals.
    total_classes = exporter_metrics.get("total", {}).get("storage_classes", {})
    total_errors = []
    for sc in set(expected_totals) | set(total_classes):
        total_errors.extend(_class_checks(f"Total [{sc}]",
                                           expected_totals.get(sc, class_metrics()),
                                           total_classes.get(sc, {})))
    if total_errors:
        raise AssertionError(f"{label} total mismatch:\n" + "\n".join(total_errors))

    expected_total_dm = sum(b["delete_markers"] for b in actual_state.values())
    actual_total_dm = exporter_metrics.get("total_delete_markers", 0)
    if actual_total_dm != expected_total_dm:
        raise AssertionError(
            f"{label}: Total delete markers mismatch. "
            f"Expected: {expected_total_dm}, Got: {actual_total_dm}"
        )

    logger.info(f"{label}: All metrics verified successfully")


def verify_with_retry(actual_state: dict, get_metrics, label: str = "check",
                      timeout: float = 30, interval: float = 3) -> None:
    """Poll ``get_metrics()`` until the exporter matches ``actual_state``.

    ``actual_state`` must be a stable snapshot (S3 is not mutated during the
    call), so the exporter can only converge toward it. Returns once
    ``verify_metrics_match_state`` passes; if it never converges within
    ``timeout``, the final attempt's AssertionError propagates with full detail.
    This replaces fixed post-mutation sleeps: it finishes as soon as the next
    scrape lands and tolerates a slow/late scrape instead of asserting on a
    single stale read.
    """
    deadline = time.monotonic() + timeout
    while True:
        try:
            verify_metrics_match_state(actual_state, get_metrics(), label=label)
            return
        except AssertionError:
            if time.monotonic() >= deadline:
                raise
            time.sleep(interval)
