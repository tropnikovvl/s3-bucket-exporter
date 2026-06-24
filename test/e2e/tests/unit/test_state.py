import pytest

from e2elib.state import get_actual_bucket_state


class _FakePaginator:
    def __init__(self, pages):
        self._pages = pages

    def paginate(self, Bucket):
        return self._pages[Bucket]


class _FakeClient:
    def __init__(self, pages):
        self._pages = pages

    def get_paginator(self, name):
        assert name == "list_object_versions"
        return _FakePaginator(self._pages)


def test_state_counts_current_noncurrent_and_markers():
    pages = {"b1": [{
        "Versions": [
            {"IsLatest": True, "Size": 100},
            {"IsLatest": False, "Size": 30},
            {"IsLatest": False, "Size": 20},
        ],
        "DeleteMarkers": [{"Key": "x"}, {"Key": "y"}],
    }]}
    state = get_actual_bucket_state(_FakeClient(pages), ["b1"])
    assert state["b1"] == {
        "current_count": 1, "current_size": 100,
        "noncurrent_count": 2, "noncurrent_size": 50,
        "delete_markers": 2,
    }


def test_state_empty_bucket():
    state = get_actual_bucket_state(_FakeClient({"b": [{}]}), ["b"])
    assert state["b"] == {
        "current_count": 0, "current_size": 0,
        "noncurrent_count": 0, "noncurrent_size": 0, "delete_markers": 0,
    }


def test_state_propagates_listing_errors():
    class _Boom:
        def get_paginator(self, name):
            raise RuntimeError("boom")

    with pytest.raises(RuntimeError):
        get_actual_bucket_state(_Boom(), ["b"])
