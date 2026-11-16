"""Local dense BGE-M3 embeddings, with one model and inference lock per process."""

from dataclasses import dataclass
from collections import OrderedDict
from functools import lru_cache
import logging
import json
import math
import os
import threading
import time

from .embeddings import EmbeddingError, normalize_vector


logger = logging.getLogger(__name__)
_MODEL_LOCK = threading.Lock()
DEFAULT_REVISION = "5617a9f61b028005a4858fdac845db406aefb181"


@dataclass(frozen=True)
class LocalEmbeddingConfig:
    model: str = "BAAI/bge-m3"
    dimensions: int = 1024
    batch_size: int = 4
    device: str = "cpu"
    revision: str = DEFAULT_REVISION
    cache_dir: str | None = None
    local_files_only: bool = False

    def __post_init__(self):
        for name in ("model", "device", "revision"):
            if not isinstance(getattr(self, name), str) or not getattr(self, name).strip():
                raise ValueError(f"Invalid local embedding {name}")
        if type(self.dimensions) is not int or not 1 <= self.dimensions <= 2000:
            raise ValueError("RAG_EMBEDDING_DIMENSIONS must be between 1 and 2000")
        if self.model == "BAAI/bge-m3" and self.dimensions != 1024:
            raise ValueError("BAAI/bge-m3 requires RAG_EMBEDDING_DIMENSIONS=1024")
        if type(self.batch_size) is not int or not 1 <= self.batch_size <= 128:
            raise ValueError("RAG_EMBEDDING_BATCH_SIZE must be between 1 and 128")

    @property
    def identity(self):
        return json.dumps(["local-dense", self.model, self.revision, self.dimensions], separators=(",", ":"))

    @classmethod
    def from_env(cls):
        return cls(
            model=os.environ.get("RAG_EMBEDDING_MODEL", cls.model),
            dimensions=int(os.environ.get("RAG_EMBEDDING_DIMENSIONS", "1024")),
            batch_size=int(os.environ.get("RAG_EMBEDDING_BATCH_SIZE", "4")),
            device=os.environ.get("RAG_EMBEDDING_DEVICE", "cpu"),
            revision=os.environ.get("RAG_EMBEDDING_REVISION", cls.revision),
            cache_dir=os.environ.get("RAG_EMBEDDING_CACHE_DIR") or None,
            local_files_only=os.environ.get("HF_HUB_OFFLINE", "0").lower() in ("1", "true"),
        )


@lru_cache(maxsize=1)
def _load_model(model, revision, device, cache_dir, local_files_only):
    try:
        from sentence_transformers import SentenceTransformer
    except ImportError:
        raise EmbeddingError("Install the local embedding dependencies: pip install 'ai-buddy-rag[local]'") from None
    logger.info("Loading local embedding model %s on %s", model, device)
    try:
        return SentenceTransformer(model, revision=revision, device=device, cache_folder=cache_dir,
                                   local_files_only=local_files_only, trust_remote_code=False)
    except (OSError, RuntimeError, ValueError):
        raise EmbeddingError("Could not load the local embedding model; check dependencies, model cache and memory") from None


class LocalEmbeddingClient:
    def __init__(self, config=None):
        self.config = config if config is not None else LocalEmbeddingConfig.from_env()
        self._cache = OrderedDict()

    def _model(self):
        config = self.config
        model = _load_model(config.model, config.revision, config.device,
                            config.cache_dir, config.local_files_only)
        if model.get_sentence_embedding_dimension() != config.dimensions:
            raise EmbeddingError("Local model output does not match RAG_EMBEDDING_DIMENSIONS")
        return model

    def warmup(self):
        with _MODEL_LOCK:
            self._model()

    def embed(self, texts, *, timeout_seconds=None):
        if not texts:
            return []
        if any(not isinstance(text, str) or not text.strip() for text in texts):
            raise EmbeddingError("Cannot embed empty text")
        if timeout_seconds is not None and (not math.isfinite(timeout_seconds) or timeout_seconds <= 0):
            raise EmbeddingError("Embedding deadline exceeded")
        deadline = None if timeout_seconds is None else time.monotonic() + timeout_seconds

        def remaining():
            budget = None if deadline is None else deadline - time.monotonic()
            if budget is not None and budget <= 0:
                raise EmbeddingError("Embedding deadline exceeded")
            return budget

        budget = remaining()
        acquired = _MODEL_LOCK.acquire() if budget is None else _MODEL_LOCK.acquire(timeout=budget)
        if not acquired:
            raise EmbeddingError("Embedding deadline exceeded while waiting for the local model")
        try:
            key = tuple(texts)
            if key in self._cache:
                self._cache.move_to_end(key)
                return [list(vector) for vector in self._cache[key]]
            model = self._model()
            vectors = []
            for offset in range(0, len(texts), self.config.batch_size):
                remaining()
                batch = texts[offset:offset + self.config.batch_size]
                result = model.encode(batch, batch_size=self.config.batch_size, normalize_embeddings=True,
                                      show_progress_bar=False, convert_to_numpy=True).tolist()
                remaining()
                if len(result) != len(batch):
                    raise EmbeddingError("Local embedding count did not match the input")
                vectors.extend(normalize_vector(vector, self.config.dimensions) for vector in result)
            self._cache[key] = tuple(tuple(vector) for vector in vectors)
            if len(self._cache) > 32:
                self._cache.popitem(last=False)
            return vectors
        except (OSError, RuntimeError, ValueError) as exc:
            if isinstance(exc, EmbeddingError):
                raise
            raise EmbeddingError("Local embedding inference failed; check the model and available memory") from None
        finally:
            _MODEL_LOCK.release()
