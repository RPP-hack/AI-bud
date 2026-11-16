"""Public read-only entry point for a backend with an authenticated employee context."""

from dataclasses import replace

from .access import PROFILES
from .generation import Generator
from .retrieval import Retriever


def ask(question, *, audience, store=None, generator=None, offline=False, top_k=3, as_of=None,
        timeout_seconds=None):
    passages = Retriever(store).retrieve(question, audience=audience, top_k=top_k, as_of=as_of,
                                         timeout_seconds=timeout_seconds)
    result = (generator or Generator()).generate(question, passages, audience=audience, offline=offline)
    if (audience == PROFILES["intern-platform"] and result.status in ("answered", "sources_found")
            and passages and passages[0].rule_key == "jira.bud.read" and not passages[0].stale
            and any(p.document_id == "jira-access" and p.rule_key == "jira.bud.read" and not p.stale
                    for p in result.sources.values())):
        return replace(result, action="preview_jira_access")
    return result
