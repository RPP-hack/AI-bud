"""Split source pages into bounded passages, keeping exact character offsets."""

from dataclasses import dataclass
from hashlib import sha256

from .loaders import DocumentPage


@dataclass(frozen=True)
class Chunk:
    chunk_id: str
    text: str
    page_number: int | None
    start_char: int
    end_char: int
    extraction_method: str


def chunk_pages(pages: list[DocumentPage], *, chunk_size=1400, chunk_overlap=160) -> list[Chunk]:
    """Prefer paragraph and sentence boundaries. Sizes and overlap are in characters."""
    if not 0 <= chunk_overlap < chunk_size:
        raise ValueError("Expected 0 <= chunk_overlap < chunk_size")
    chunks = []
    for page in pages:
        text = page.text
        start = 0
        while start < len(text):
            end = min(start + chunk_size, len(text))
            if end < len(text):
                # Avoid short fragments and guarantee forward progress after overlap.
                lower = start + max(chunk_size // 2, chunk_overlap)
                for separator in ("\n\n", "\n", ". ", " "):
                    cut = text.rfind(separator, lower, end)
                    if cut >= 0:
                        end = cut + len(separator)
                        break
            passage = text[start:end]
            if passage.strip():
                identity = f"{page.page_number}:{start}:{end}:{passage}"
                chunks.append(Chunk(sha256(identity.encode()).hexdigest(), passage,
                                    page.page_number, start, end, page.extraction_method))
            if end == len(text):
                break
            start = end - chunk_overlap
    return chunks
