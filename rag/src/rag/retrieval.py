"""Find onboarding evidence visible to the authenticated employee."""

from dataclasses import dataclass
from datetime import date
import json

from .storage import Audience, create_store
from .guardrails import validate_question


@dataclass(frozen=True)
class RetrievedChunk:
    version_id: str
    document_id: str
    chunk_id: str
    text: str
    source_type: str
    source_path: str
    origin: str
    title: str
    source_url: str | None
    team: str
    roles: tuple[str, ...]
    review_by: str | None
    stale: bool
    rule_key: str | None
    rule_value: str | None
    page_number: int | None
    start_char: int
    end_char: int
    extraction_method: str

    @property
    def citation_id(self):
        return f"{self.version_id}:{self.chunk_id}"


class Retriever:
    def __init__(self, store=None):
        self.store = store if store is not None else create_store()

    def retrieve(self, query: str, *, audience: Audience, top_k=3, as_of=None,
                 timeout_seconds=None) -> list[RetrievedChunk]:
        validate_question(query)
        if type(top_k) is not int or not 1 <= top_k <= 50:
            raise ValueError("top_k must be between 1 and 50")
        if not isinstance(audience, Audience):
            raise ValueError("An authenticated audience is required")
        today = date.today() if as_of is None else as_of
        if type(today) is not date:
            raise ValueError("as_of must be a date")
        results = []
        for row in self.store.search(query, audience, top_k, today, timeout_seconds=timeout_seconds):
            values = dict(row)
            for key in ("rank", "title_terms", "body_terms", "distance"):
                values.pop(key, None)
            roles = values["roles"]
            values["roles"] = tuple(json.loads(roles) if isinstance(roles, str) else roles)
            stale = values["review_by"] is not None and values["review_by"] < today.isoformat()
            results.append(RetrievedChunk(**values, stale=stale))
        return results


if __name__ == "__main__":
    import sys
    from .__main__ import main
    main(["search", *sys.argv[1:]])
