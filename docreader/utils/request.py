import contextlib
import logging
import re
import time
import uuid
from collections.abc import Iterator
from contextvars import ContextVar
from logging import LogRecord

# 配置日志
logger = logging.getLogger(__name__)

# 定义上下文变量
request_id_var: ContextVar[str | None] = ContextVar("request_id", default=None)
_request_start_time_ctx: ContextVar[float | None] = ContextVar("request_start_time", default=None)
_REQ_ID_CTRL_RE = re.compile(r"[\r\n\x00-\x1f\x7f]+")


def _sanitize_request_id(request_id: str | None) -> str:
    cleaned = _REQ_ID_CTRL_RE.sub("_", str(request_id or "")).strip()
    return cleaned[:128] if cleaned else str(uuid.uuid4())


def set_request_id(request_id: str | None) -> None:
    """设置当前上下文的请求ID"""
    request_id_var.set(_sanitize_request_id(request_id))


def get_request_id() -> str | None:
    """获取当前上下文的请求ID"""
    return request_id_var.get()


class MillisecondFormatter(logging.Formatter):
    """自定义日志格式化器，只显示毫秒级时间戳(3位数字)而不是微秒(6位)"""

    def formatTime(self, record: LogRecord, datefmt: str | None = None) -> str:
        """重写formatTime方法，将微秒格式化为毫秒"""
        result = super().formatTime(record, datefmt)

        if datefmt and ".%f" in datefmt:
            parts = result.split(".")
            if len(parts) > 1 and len(parts[1]) >= 6:
                millis = parts[1][:3]
                result = f"{parts[0]}.{millis}"

        return result


def init_logging_request_id() -> None:
    """Initialize logging to include request ID in log messages."""
    logger.info("Initializing request ID logging")
    root_logger = logging.getLogger()
    fmt = "%(asctime)s.%(msecs)03d [%(request_id)s] %(levelname)-5s %(name)-20s | %(message)s"
    datefmt = "%Y-%m-%d %H:%M:%S"

    for existing_handler in root_logger.handlers:
        if not any(isinstance(f, RequestIdFilter) for f in existing_handler.filters):
            existing_handler.addFilter(RequestIdFilter())
        existing_handler.setFormatter(logging.Formatter(fmt=fmt, datefmt=datefmt))

    logger.info(
        "Updated %d handlers with request ID formatting",
        len(root_logger.handlers),
    )

    if not root_logger.handlers:
        new_handler = logging.StreamHandler()
        new_handler.setFormatter(logging.Formatter(fmt=fmt, datefmt=datefmt))
        new_handler.addFilter(RequestIdFilter())
        root_logger.addHandler(new_handler)
        logger.info("Added new StreamHandler with request ID formatting")


class RequestIdFilter(logging.Filter):
    """Filter that adds request ID to log messages"""

    def filter(self, record: LogRecord) -> bool:
        request_id = request_id_var.get()
        if request_id is not None:
            if len(request_id) > 8:
                short_id = request_id[:8]
                if "-" in request_id:
                    parts = request_id.split("-")
                    if len(parts) >= 3:
                        short_id = f"{parts[0]}-{parts[1]}-{parts[2]}"
                record.request_id = short_id
            else:
                record.request_id = request_id

            start_time = _request_start_time_ctx.get()
            if start_time is not None:
                elapsed_ms = int((time.time() - start_time) * 1000)
                record.elapsed_ms = elapsed_ms
                if not hasattr(record, "message_with_elapsed"):
                    record.message_with_elapsed = True
                    record.msg = f"{record.msg} (elapsed: {elapsed_ms}ms)"
        else:
            record.request_id = "no-req-id"

        return True


@contextlib.contextmanager
def request_id_context(request_id: str | None = None) -> Iterator[str | None]:
    """Context manager that sets a request ID for the current context."""
    req_id = _sanitize_request_id(request_id) if request_id else str(uuid.uuid4())

    start_time = time.time()
    req_token = request_id_var.set(req_id)
    time_token = _request_start_time_ctx.set(start_time)

    logger.info("Starting new request with ID: %s", req_id)

    try:
        yield request_id_var.get()
    finally:
        elapsed_ms = int((time.time() - start_time) * 1000)
        logger.info("Request %s completed in %dms", req_id, elapsed_ms)
        request_id_var.reset(req_token)
        _request_start_time_ctx.reset(time_token)
