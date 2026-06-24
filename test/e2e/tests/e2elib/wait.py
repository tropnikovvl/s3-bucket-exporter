"""Active polling helper to replace fixed sleeps."""
import logging
import time
from typing import Callable

logger = logging.getLogger(__name__)


def wait_until(predicate: Callable[[], bool], timeout: float = 30, interval: float = 1) -> None:
    """Poll ``predicate`` until truthy or ``timeout`` seconds elapse.

    Exceptions raised by ``predicate`` are treated as "not ready yet".
    Raises ``TimeoutError`` if the condition is never met.
    """
    deadline = time.monotonic() + timeout
    last_error: Exception | None = None
    while time.monotonic() < deadline:
        try:
            if predicate():
                return
        except Exception as e:  # not ready; keep polling
            last_error = e
        time.sleep(interval)
    raise TimeoutError(f"Condition not met within {timeout}s (last error: {last_error})")
