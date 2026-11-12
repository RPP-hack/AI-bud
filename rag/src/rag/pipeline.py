"""Public read-only entry point for a backend with an authenticated employee context."""

from dataclasses import replace

from .access import PROFILES
from .generation import Generator
from .retrieval import Retriever
from .terms import search_terms


def ask(question, *, audience, store=None, generator=None, offline=False, top_k=3):
    passages = Retriever(store).retrieve(question, audience=audience, top_k=top_k)
    result = (generator or Generator()).generate(question, passages, audience=audience, offline=offline)
    if (audience == PROFILES["intern-platform"] and result.status in ("answered", "sources_found")
            and {"jira", "403", "bud", "доступ"} & set(search_terms(question))
            and any(p.document_id == "jira-access" and not p.stale for p in result.sources.values())):
        return replace(result, action="preview_jira_access")
    return result
