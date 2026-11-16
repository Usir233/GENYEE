"""Standalone Reranker HTTP service (hardened defaults).

Security defaults (GENYEE-MED-13 / GENYEE-MED-20):
- Binds to ``127.0.0.1`` by default (override via ``RERANK_HOST``).
- Fail-closed Bearer / ``X-API-Key`` authentication via ``RERANK_API_KEY`` using
  constant-time ``hmac.compare_digest`` (opt-out only for isolated local dev via
  ``RERANK_ALLOW_UNAUTHENTICATED=true``).
- Bounds ``documents`` batch count and character lengths to prevent OOM DoS.
- Lazy model loading (no hardcoded user path or import-time ``exit()``),
  ``trust_remote_code=False`` by default, ``use_safetensors=True``, and optional
  pinned ``RERANK_MODEL_REVISION``.
"""

from __future__ import annotations

from contextlib import asynccontextmanager
import gc
import hmac
import logging
import os
import re
from typing import List, Optional

from fastapi import Depends, FastAPI, Header, HTTPException
from pydantic import BaseModel, Field

logger = logging.getLogger(__name__)

_COMMIT_SHA_RE = re.compile(r"^[0-9a-f]{40}$")


def _env_positive_int(name: str, default: int) -> int:
    raw = os.getenv(name)
    if raw is None or not raw.strip():
        return default
    try:
        parsed = int(raw.strip())
    except ValueError:
        return default
    return parsed if parsed > 0 else default


MAX_RERANK_DOCUMENTS = _env_positive_int("RERANK_MAX_DOCUMENTS", 128)
MAX_QUERY_CHARS = _env_positive_int("RERANK_MAX_QUERY_CHARS", 8192)
MAX_DOCUMENT_CHARS = _env_positive_int("RERANK_MAX_DOCUMENT_CHARS", 16384)
MIN_API_KEY_LENGTH = 16


def _env_truthy(name: str, default: bool = False) -> bool:
    raw = os.getenv(name)
    if raw is None:
        return default
    return raw.strip().lower() in {"1", "true", "yes", "on"}


class RerankRequest(BaseModel):
    query: str = Field(..., min_length=1, max_length=MAX_QUERY_CHARS)
    documents: List[str] = Field(
        ..., min_length=1, max_length=MAX_RERANK_DOCUMENTS
    )


class DocumentInfo(BaseModel):
    text: str


class TestRankResult(BaseModel):
    index: int
    document: DocumentInfo
    score: float


class TestFinalResponse(BaseModel):
    results: List[TestRankResult]


class _RerankRuntime:
    def __init__(self) -> None:
        self.model = None
        self.tokenizer = None
        self.device = None

    def ensure_loaded(self) -> None:
        if self.model is not None and self.tokenizer is not None:
            return
        model_path = os.getenv("RERANK_MODEL_PATH", "").strip()
        if not model_path:
            raise RuntimeError(
                "RERANK_MODEL_PATH environment variable must be configured"
            )
        trust_remote_code = _env_truthy("RERANK_TRUST_REMOTE_CODE", default=False)
        revision = os.getenv("RERANK_MODEL_REVISION", "").strip() or None
        is_local_dir = os.path.isdir(model_path)
        local_files_only = _env_truthy(
            "RERANK_LOCAL_FILES_ONLY", default=is_local_dir
        )
        if trust_remote_code:
            if not is_local_dir:
                if not revision or not _COMMIT_SHA_RE.match(revision):
                    raise RuntimeError(
                        "RERANK_MODEL_REVISION must be a 40-char lowercase hex commit SHA "
                        "when RERANK_TRUST_REMOTE_CODE=true for a remote repository"
                    )
            elif not (revision and _COMMIT_SHA_RE.match(revision)):
                local_files_only = True

        import torch
        from transformers import AutoModelForSequenceClassification, AutoTokenizer

        self.device = torch.device("cuda" if torch.cuda.is_available() else "cpu")
        common_kwargs = {
            "trust_remote_code": trust_remote_code,
            "local_files_only": local_files_only,
        }
        if revision:
            common_kwargs["revision"] = revision

        self.tokenizer = AutoTokenizer.from_pretrained(model_path, **common_kwargs)
        self.model = AutoModelForSequenceClassification.from_pretrained(
            model_path,
            use_safetensors=True,
            **common_kwargs,
        )
        self.model.to(self.device)
        self.model.eval()


_runtime = _RerankRuntime()


def verify_rerank_auth(
    authorization: Optional[str] = Header(default=None),
    x_api_key: Optional[str] = Header(default=None, alias="X-API-Key"),
) -> None:
    expected = os.getenv("RERANK_API_KEY", "").strip()
    allow_unauth = _env_truthy("RERANK_ALLOW_UNAUTHENTICATED", default=False)
    if not expected:
        if allow_unauth:
            return
        raise HTTPException(
            status_code=401,
            detail="RERANK_API_KEY is not configured; unauthenticated access denied.",
            headers={"WWW-Authenticate": "Bearer"},
        )

    provided = ""
    if authorization and authorization.lower().startswith("bearer "):
        provided = authorization[7:].strip()
    elif x_api_key:
        provided = x_api_key.strip()

    if not provided or not hmac.compare_digest(
        provided.encode("utf-8"), expected.encode("utf-8")
    ):
        raise HTTPException(
            status_code=401,
            detail="Invalid or missing API key.",
            headers={"WWW-Authenticate": "Bearer"},
        )


@asynccontextmanager
async def _lifespan(_app: FastAPI):
    if os.getenv("RERANK_MODEL_PATH", "").strip():
        _runtime.ensure_loaded()
    yield


app = FastAPI(
    title="Reranker API",
    description="Authenticated reranking service with bounded batch execution",
    version="1.1.0",
    lifespan=_lifespan,
)


@app.post(
    "/rerank",
    response_model=TestFinalResponse,
    dependencies=[Depends(verify_rerank_auth)],
)
def rerank_endpoint(request: RerankRequest):
    for doc in request.documents:
        if len(doc) > MAX_DOCUMENT_CHARS:
            raise HTTPException(
                status_code=400,
                detail=f"Document exceeds maximum length ({MAX_DOCUMENT_CHARS} chars).",
            )

    try:
        _runtime.ensure_loaded()
    except RuntimeError as exc:
        logger.error("Rerank model unavailable: %s", exc)
        raise HTTPException(status_code=503, detail="Reranker model is not loaded.") from None

    import torch

    pairs = [[request.query, doc] for doc in request.documents]
    with torch.no_grad():
        inputs = outputs = logits = None
        try:
            inputs = _runtime.tokenizer(
                pairs,
                padding=True,
                truncation=True,
                return_tensors="pt",
                max_length=1024,
            ).to(_runtime.device)
            outputs = _runtime.model(**inputs, return_dict=True)
            logits = outputs.logits.view(-1).float()
            scores = torch.sigmoid(logits).cpu().tolist()
        finally:
            del inputs, outputs, logits
            gc.collect()
            if torch.cuda.is_available():
                torch.cuda.empty_cache()
            elif hasattr(torch, "mps") and torch.mps.is_available():
                torch.mps.empty_cache()

    results = [
        TestRankResult(
            index=i,
            document=DocumentInfo(text=text),
            score=float(score_val),
        )
        for i, (text, score_val) in enumerate(zip(request.documents, scores))
    ]
    sorted_results = sorted(results, key=lambda x: x.score, reverse=True)
    return {"results": sorted_results}


@app.get("/health")
def read_health():
    return {"status": "ok", "model_loaded": _runtime.model is not None}


@app.get("/", dependencies=[Depends(verify_rerank_auth)])
def read_root():
    return {"status": "Reranker API is running"}


def main() -> None:
    import uvicorn

    host = os.getenv("RERANK_HOST", "127.0.0.1").strip() or "127.0.0.1"
    port = _env_positive_int("RERANK_PORT", 8000)
    api_key = os.getenv("RERANK_API_KEY", "").strip()
    allow_unauth = _env_truthy("RERANK_ALLOW_UNAUTHENTICATED", default=False)
    if not api_key and not allow_unauth:
        raise SystemExit(
            "Refusing to start rerank server without RERANK_API_KEY "
            "(set RERANK_ALLOW_UNAUTHENTICATED=true only for isolated local testing)."
        )
    if api_key and len(api_key) < MIN_API_KEY_LENGTH:
        raise SystemExit(
            f"RERANK_API_KEY must be at least {MIN_API_KEY_LENGTH} characters."
        )
    uvicorn.run(app, host=host, port=port)


if __name__ == "__main__":
    main()
