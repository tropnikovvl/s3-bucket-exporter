import pytest

from e2elib.verify import verify_metrics_match_state


def _cls(cc=0, cs=0, ncc=0, ncs=0):
    return {"current_count": cc, "current_size": cs,
            "noncurrent_count": ncc, "noncurrent_size": ncs}


def _em_cls(cc=0, cs=0, ncc=0, ncs=0):
    return {"object_count": {"current": cc, "noncurrent": ncc},
            "total_size": {"current": cs, "noncurrent": ncs}}


def _metrics(buckets, totals, bucket_count, dm_total=0):
    m = {"endpoint_up": 1, "bucket_count": bucket_count,
         "total": {"storage_classes": totals}, "total_delete_markers": dm_total}
    m.update(buckets)
    return m


def test_verify_passes_with_multiple_classes():
    state = {"b1": {"storage_classes": {
        "STANDARD": _cls(cc=2, cs=2048),
        "GLACIER": _cls(cc=1, cs=500),
    }, "delete_markers": 0}}
    metrics = _metrics(
        {"b1": {"storage_classes": {
            "STANDARD": _em_cls(cc=2, cs=2048),
            "GLACIER": _em_cls(cc=1, cs=500),
        }, "delete_markers": 0}},
        {"STANDARD": _em_cls(cc=2, cs=2048), "GLACIER": _em_cls(cc=1, cs=500)},
        bucket_count=1,
    )
    verify_metrics_match_state(state, metrics, label="t")  # no raise


def test_verify_fails_on_per_class_mismatch():
    state = {"b1": {"storage_classes": {"GLACIER": _cls(cc=1, cs=500)}, "delete_markers": 0}}
    metrics = _metrics(
        {"b1": {"storage_classes": {"GLACIER": _em_cls(cc=1, cs=999)}, "delete_markers": 0}},
        {"GLACIER": _em_cls(cc=1, cs=999)}, bucket_count=1)
    with pytest.raises(AssertionError):
        verify_metrics_match_state(state, metrics, label="t")


def test_verify_fails_on_phantom_exporter_class():
    # exporter reports a class the actual state does not have
    state = {"b1": {"storage_classes": {"STANDARD": _cls(cc=1, cs=10)}, "delete_markers": 0}}
    metrics = _metrics(
        {"b1": {"storage_classes": {
            "STANDARD": _em_cls(cc=1, cs=10),
            "GLACIER": _em_cls(cc=1, cs=10),
        }, "delete_markers": 0}},
        {"STANDARD": _em_cls(cc=1, cs=10), "GLACIER": _em_cls(cc=1, cs=10)},
        bucket_count=1)
    with pytest.raises(AssertionError):
        verify_metrics_match_state(state, metrics, label="t")


def test_verify_checks_delete_markers_and_endpoint():
    state = {"b1": {"storage_classes": {"STANDARD": _cls(cc=1, cs=10)}, "delete_markers": 3}}
    metrics = _metrics(
        {"b1": {"storage_classes": {"STANDARD": _em_cls(cc=1, cs=10)}, "delete_markers": 3}},
        {"STANDARD": _em_cls(cc=1, cs=10)}, bucket_count=1, dm_total=3)
    verify_metrics_match_state(state, metrics, label="t")  # no raise
    metrics["endpoint_up"] = 0
    with pytest.raises(AssertionError):
        verify_metrics_match_state(state, metrics, label="t")
