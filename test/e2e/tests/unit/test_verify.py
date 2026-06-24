import pytest

from e2elib.verify import verify_metrics_match_state


def _state(**kw):
    base = dict(current_count=0, current_size=0, noncurrent_count=0,
                noncurrent_size=0, delete_markers=0)
    base.update(kw)
    return base


def _metrics_for(bucket, current_count, current_size, bucket_count=1):
    return {
        "endpoint_up": 1,
        "bucket_count": bucket_count,
        bucket: {"storage_classes": {"STANDARD": {
            "object_count": {"current": current_count, "noncurrent": 0},
            "total_size": {"current": current_size, "noncurrent": 0},
        }}, "delete_markers": 0},
        "total": {"storage_classes": {"STANDARD": {
            "object_count": {"current": current_count, "noncurrent": 0},
            "total_size": {"current": current_size, "noncurrent": 0},
        }}},
        "total_delete_markers": 0,
    }


def test_verify_passes_when_matching():
    state = {"b1": _state(current_count=2, current_size=2048)}
    metrics = _metrics_for("b1", 2, 2048)
    verify_metrics_match_state(state, metrics, label="t")  # no raise


def test_verify_fails_on_count_mismatch():
    state = {"b1": _state(current_count=5, current_size=2048)}
    metrics = _metrics_for("b1", 2, 2048)
    with pytest.raises(AssertionError):
        verify_metrics_match_state(state, metrics, label="t")


def test_verify_fails_when_endpoint_down():
    state = {"b1": _state()}
    metrics = _metrics_for("b1", 0, 0)
    metrics["endpoint_up"] = 0
    with pytest.raises(AssertionError):
        verify_metrics_match_state(state, metrics, label="t")
