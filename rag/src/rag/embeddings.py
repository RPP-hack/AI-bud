"""Shared validation and factory for local embeddings."""

import math


class EmbeddingError(RuntimeError):
    pass


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


def create_embedder():
    from .local_embeddings import LocalEmbeddingClient
    return LocalEmbeddingClient()
