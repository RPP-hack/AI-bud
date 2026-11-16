"""Bounded OpenAI-compatible embedding requests, independent of chat generation."""

from dataclasses import dataclass, field
from http.client import HTTPException
import json
import math
import os
from urllib.error import HTTPError, URLError
from urllib.parse import urlsplit
from urllib.request import HTTPRedirectHandler, Request, build_opener


MAX_RESPONSE_BYTES = 8 * 1024 * 1024


class EmbeddingError(RuntimeError):
    pass


@dataclass(frozen=True)
class EmbeddingConfig:
    api_key: str = field(default="", repr=False)
    model: str = "openai/text-embedding-3-small"
    base_url: str = "https://openrouter.ai/api/v1"
    dimensions: int = 1536
    batch_size: int = 32
    timeout_seconds: float = 20

    def __post_init__(self):
        if not isinstance(self.api_key, str) or any(c.isspace() for c in self.api_key):
            raise ValueError("Invalid RAG_EMBEDDING_API_KEY")
        if not isinstance(self.model, str) or not self.model.strip():
            raise ValueError("Set RAG_EMBEDDING_MODEL")
        url = urlsplit(self.base_url)
        if (not url.hostname or url.username or url.password or url.query or url.fragment
                or not (url.scheme == "https" or
                        (url.scheme == "http" and url.hostname in ("localhost", "127.0.0.1", "::1")))):
            raise ValueError("Use an HTTPS embedding endpoint or local HTTP")
        object.__setattr__(self, "base_url", self.base_url.rstrip("/"))
        if type(self.dimensions) is not int or not 1 <= self.dimensions <= 2000:
            raise ValueError("RAG_EMBEDDING_DIMENSIONS must be between 1 and 2000")
        if type(self.batch_size) is not int or not 1 <= self.batch_size <= 128:
            raise ValueError("Embedding batch_size must be between 1 and 128")
        if (type(self.timeout_seconds) not in (int, float)
                or not math.isfinite(self.timeout_seconds) or self.timeout_seconds <= 0):
            raise ValueError("Invalid embedding timeout")

    @property
    def requires_api_key(self):
        return True

    @property
    def identity(self):
        # Never mix vectors produced by different models/providers or dimensions.
        return json.dumps([self.base_url, self.model, self.dimensions], separators=(",", ":"))

    @classmethod
    def from_env(cls):
        return cls(
            api_key=os.environ.get("RAG_EMBEDDING_API_KEY") or os.environ.get("RAG_LLM_API_KEY", ""),
            model=os.environ.get("RAG_EMBEDDING_MODEL", cls.model),
            base_url=os.environ.get("RAG_EMBEDDING_BASE_URL", cls.base_url),
            dimensions=int(os.environ.get("RAG_EMBEDDING_DIMENSIONS", "1536")),
        )


def normalize_vector(vector, dimensions):
    try:
        valid = (isinstance(vector, (list, tuple)) and len(vector) == dimensions
                 and all(type(v) in (int, float) and math.isfinite(v) for v in vector))
    except OverflowError:
        valid = False
    if not valid:
        raise EmbeddingError("Invalid embedding shape or values")
    norm = math.hypot(*vector)
    if not math.isfinite(norm) or norm == 0:
        raise EmbeddingError("Invalid embedding norm")
    return [float(v / norm) for v in vector]


class _NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, request, fp, code, msg, headers, new_url):
        return None


class EmbeddingClient:
    def __init__(self, config=None):
        self.config = config if config is not None else EmbeddingConfig.from_env()

    def embed(self, texts, *, timeout_seconds=None):
        if not texts:
            return []
        if not self.config.api_key:
            raise EmbeddingError("Set RAG_EMBEDDING_API_KEY or RAG_LLM_API_KEY")
        if any(not isinstance(text, str) or not text.strip() for text in texts):
            raise EmbeddingError("Cannot embed empty text")
        timeout = self.config.timeout_seconds
        if timeout_seconds is not None:
            if not math.isfinite(timeout_seconds) or timeout_seconds <= 0:
                raise EmbeddingError("Embedding deadline exceeded")
            timeout = min(timeout, timeout_seconds)
        vectors = []
        for offset in range(0, len(texts), self.config.batch_size):
            batch = texts[offset:offset + self.config.batch_size]
            request = Request(self.config.base_url + "/embeddings", data=json.dumps({
                "model": self.config.model, "input": batch,
                "dimensions": self.config.dimensions, "encoding_format": "float",
            }, ensure_ascii=False).encode("utf-8"), headers={
                "Authorization": "Bearer " + self.config.api_key,
                "Content-Type": "application/json", "Accept": "application/json",
            }, method="POST")
            try:
                with build_opener(_NoRedirect()).open(request, timeout=timeout) as response:
                    body = response.read(MAX_RESPONSE_BYTES + 1)
            except HTTPError as exc:
                status = exc.code
                exc.close()
                raise EmbeddingError(f"Embedding gateway returned HTTP {status}") from None
            except (URLError, TimeoutError, OSError, HTTPException):
                raise EmbeddingError("Embedding gateway unavailable") from None
            if len(body) > MAX_RESPONSE_BYTES:
                raise EmbeddingError("Embedding response exceeded the size limit")
            try:
                data = json.loads(body)["data"]
                if (not isinstance(data, list) or len(data) != len(batch)
                        or any(type(item["index"]) is not int for item in data)
                        or {item["index"] for item in data} != set(range(len(batch)))):
                    raise ValueError
                ordered = sorted(data, key=lambda item: item["index"])
                vectors.extend(normalize_vector(item["embedding"], self.config.dimensions)
                               for item in ordered)
            except (ValueError, KeyError, TypeError, UnicodeDecodeError, RecursionError):
                raise EmbeddingError("Invalid embedding gateway response") from None
        return vectors


def create_embedder():
    provider = os.environ.get("RAG_EMBEDDING_PROVIDER", "local")
    if provider == "local":
        from .local_embeddings import LocalEmbeddingClient
        return LocalEmbeddingClient()
    if provider == "api":
        return EmbeddingClient()
    raise ValueError("RAG_EMBEDDING_PROVIDER must be local or api")
