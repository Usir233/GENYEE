import logging
import threading
import time
from contextlib import contextmanager
from typing import Any, Dict, Iterator, Optional

logger = logging.getLogger(__name__)

_LIMITERS: Dict[str, threading.BoundedSemaphore] = {}
_LIMITERS_LOCK = threading.Lock()


def check_cancellation(context: Any = None) -> None:
    """Check if the given gRPC context has been cancelled or deadline exceeded."""
    if context is None:
        return
    if hasattr(context, "is_active") and not context.is_active():
        raise TimeoutError("gRPC call was cancelled by client")
    if hasattr(context, "time_remaining"):
        rem = context.time_remaining()
        if rem is not None and rem <= 0:
            raise TimeoutError("gRPC call deadline exceeded")


def _get_limiter(name: str, max_workers: int) -> threading.BoundedSemaphore:
    with _LIMITERS_LOCK:
        limiter = _LIMITERS.get(name)
        if limiter is None:
            limiter = threading.BoundedSemaphore(max_workers)
            _LIMITERS[name] = limiter
        return limiter


@contextmanager
def parser_worker_limit(
    name: str,
    max_workers: int,
    timeout: Optional[float] = None,
    context: Any = None,
) -> Iterator[None]:
    """Limit concurrent access to heavy, process-wide parser operations.

    Set max_workers <= 0 to disable throttling for deployments that have enough
    CPU/GPU resources and know the parser backend is safe under concurrency.
    Supports timeout and gRPC context cancellation checks.
    """

    if max_workers <= 0:
        check_cancellation(context)
        yield
        return

    limiter = _get_limiter(name, max_workers)
    logger.debug("Waiting for %s parser slot (max_workers=%d)", name, max_workers)

    check_cancellation(context)
    acquire_timeout = timeout
    if context is not None and hasattr(context, "time_remaining"):
        rem = context.time_remaining()
        if rem is not None:
            if rem <= 0:
                raise TimeoutError("gRPC call deadline exceeded")
            acquire_timeout = rem if acquire_timeout is None else min(acquire_timeout, rem)

    if context is not None:
        acquired = False
        start_time = time.monotonic()
        while not acquired:
            check_cancellation(context)
            step_timeout = 0.5
            if acquire_timeout is not None:
                left = acquire_timeout - (time.monotonic() - start_time)
                if left <= 0:
                    raise TimeoutError(f"Timed out waiting for {name} parser slot")
                step_timeout = min(step_timeout, left)
            acquired = limiter.acquire(timeout=step_timeout)
    else:
        if acquire_timeout is not None:
            if not limiter.acquire(timeout=acquire_timeout):
                raise TimeoutError(f"Timed out waiting for {name} parser slot")
        else:
            limiter.acquire()

    try:
        check_cancellation(context)
        yield
    finally:
        limiter.release()
