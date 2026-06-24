import pytest

from e2elib.wait import wait_until


def test_wait_until_returns_when_predicate_true():
    calls = {"n": 0}

    def pred():
        calls["n"] += 1
        return calls["n"] >= 3

    wait_until(pred, timeout=5, interval=0)
    assert calls["n"] == 3


def test_wait_until_swallows_predicate_errors():
    calls = {"n": 0}

    def pred():
        calls["n"] += 1
        if calls["n"] < 2:
            raise RuntimeError("not ready")
        return True

    wait_until(pred, timeout=5, interval=0)
    assert calls["n"] == 2


def test_wait_until_times_out():
    with pytest.raises(TimeoutError):
        wait_until(lambda: False, timeout=0.05, interval=0.01)
