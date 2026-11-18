"""Reclaim large request buffers after their gRPC worker has returned."""

import ctypes
import gc
import sys
import threading
from collections.abc import Callable
from concurrent.futures import Future, ThreadPoolExecutor
from typing import Any, ParamSpec, TypeVar

_LARGE_REQUEST_BYTES = 16 * 1024 * 1024
_request_memory = threading.local()
_malloc_trim = (
    getattr(ctypes.CDLL(None), "malloc_trim", None) if sys.platform.startswith("linux") else None
)
if _malloc_trim is not None:
    _malloc_trim.argtypes = [ctypes.c_size_t]
    _malloc_trim.restype = ctypes.c_int

_P = ParamSpec("_P")
_R = TypeVar("_R")


def track_request_size(size: int) -> None:
    if size >= _LARGE_REQUEST_BYTES:
        _request_memory.reclaim = True


class RequestThreadPoolExecutor(ThreadPoolExecutor):
    """Collect PDF wrapper cycles and trim idle arenas after large RPC tasks."""

    def submit(
        self,
        fn: Callable[_P, _R],
        /,
        *args: _P.args,
        **kwargs: _P.kwargs,
    ) -> Future[_R]:
        return super().submit(self._run, fn, args, kwargs)

    @staticmethod
    def _run(
        fn: Callable[..., _R],
        args: tuple[Any, ...],
        kwargs: dict[str, Any],
    ) -> _R:
        try:
            return fn(*args, **kwargs)
        finally:
            if getattr(_request_memory, "reclaim", False):
                _request_memory.reclaim = False
                # Inside the RPC handler, gRPC still owns the request argument.
                # Here its worker has returned, so the input arena can be freed.
                gc.collect()
                if _malloc_trim is not None:
                    _malloc_trim(0)
