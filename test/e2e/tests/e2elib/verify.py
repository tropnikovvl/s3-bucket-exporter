"""Verify exporter metrics against the actual S3 state."""
import logging

logger = logging.getLogger(__name__)

STORAGE_CLASS = "STANDARD"


def verify_metrics_match_state(actual_state: dict, exporter_metrics: dict, label: str = "check") -> None:
    """Assert exporter metrics equal the actual S3 state. Raises AssertionError on any mismatch."""
    logger.info(f"--- {label}: Verifying metrics ---")

    assert exporter_metrics.get("endpoint_up") == 1, f"{label}: Endpoint should be up"

    # The exporter's s3_bucket_count counts ALL buckets at the endpoint, so this
    # equality assumes actual_state covers every bucket present (guaranteed by the
    # isolated test endpoint). Reusing this against a shared endpoint would need a
    # different bucket_count expectation.
    expected_bucket_count = len(actual_state)
    actual_bucket_count = exporter_metrics.get("bucket_count", 0)
    assert actual_bucket_count == expected_bucket_count, (
        f"{label}: Bucket count mismatch. Expected: {expected_bucket_count}, Got: {actual_bucket_count}"
    )

    errors = []
    for bucket, state in actual_state.items():
        bucket_data = exporter_metrics.get(bucket, {})
        bm = bucket_data.get("storage_classes", {}).get(STORAGE_CLASS, {})

        checks = [
            ("current object count", bm.get("object_count", {}).get("current", 0), state["current_count"]),
            ("current size", bm.get("total_size", {}).get("current", 0), state["current_size"]),
            ("noncurrent object count", bm.get("object_count", {}).get("noncurrent", 0), state["noncurrent_count"]),
            ("noncurrent size", bm.get("total_size", {}).get("noncurrent", 0), state["noncurrent_size"]),
            ("delete markers", bucket_data.get("delete_markers", 0), state["delete_markers"]),
        ]
        for what, actual, expected in checks:
            if actual != expected:
                errors.append(f"Bucket '{bucket}' {what} mismatch. Expected: {expected}, Got: {actual}")

    if errors:
        msg = f"{label} failed with {len(errors)} error(s):\n" + "\n".join(errors)
        logger.error(msg)
        raise AssertionError(msg)

    total = exporter_metrics.get("total", {}).get("storage_classes", {}).get(STORAGE_CLASS, {})
    total_checks = [
        ("Total current object count", total.get("object_count", {}).get("current", 0),
         sum(s["current_count"] for s in actual_state.values())),
        ("Total current size", total.get("total_size", {}).get("current", 0),
         sum(s["current_size"] for s in actual_state.values())),
        ("Total noncurrent object count", total.get("object_count", {}).get("noncurrent", 0),
         sum(s["noncurrent_count"] for s in actual_state.values())),
        ("Total noncurrent size", total.get("total_size", {}).get("noncurrent", 0),
         sum(s["noncurrent_size"] for s in actual_state.values())),
        ("Total delete markers", exporter_metrics.get("total_delete_markers", 0),
         sum(s["delete_markers"] for s in actual_state.values())),
    ]
    for what, actual, expected in total_checks:
        if actual != expected:
            raise AssertionError(f"{label}: {what} mismatch. Expected: {expected}, Got: {actual}")

    logger.info(f"{label}: All metrics verified successfully")
