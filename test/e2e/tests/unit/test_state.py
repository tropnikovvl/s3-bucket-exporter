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


def test_state_breaks_down_by_storage_class():
    pages = {"b1": [{
        "Versions": [
            {"IsLatest": True, "Size": 100, "StorageClass": "STANDARD"},
            {"IsLatest": False, "Size": 30, "StorageClass": "STANDARD"},
            {"IsLatest": True, "Size": 200, "StorageClass": "GLACIER"},
        ],
        "DeleteMarkers": [{"Key": "x"}, {"Key": "y"}],
    }]}
    state = get_actual_bucket_state(_FakeClient(pages), ["b1"])
    assert state["b1"]["delete_markers"] == 2
    assert state["b1"]["storage_classes"]["STANDARD"] == {
        "current_count": 1, "current_size": 100,
        "noncurrent_count": 1, "noncurrent_size": 30,
    }
    assert state["b1"]["storage_classes"]["GLACIER"] == {
        "current_count": 1, "current_size": 200,
        "noncurrent_count": 0, "noncurrent_size": 0,
    }


def test_state_defaults_missing_storage_class_to_standard():
    pages = {"b": [{"Versions": [{"IsLatest": True, "Size": 5}]}]}
    state = get_actual_bucket_state(_FakeClient(pages), ["b"])
    assert state["b"]["storage_classes"]["STANDARD"]["current_count"] == 1


def test_state_empty_bucket():
    state = get_actual_bucket_state(_FakeClient({"b": [{}]}), ["b"])
    assert state["b"] == {"storage_classes": {}, "delete_markers": 0}


def test_state_propagates_listing_errors():
    class _Boom:
        def get_paginator(self, name):
            raise RuntimeError("boom")

    with pytest.raises(RuntimeError):
        get_actual_bucket_state(_Boom(), ["b"])
