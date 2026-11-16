"""Public read-only entry point for a backend with an authenticated employee context."""

from dataclasses import replace
import logging
import time

from .access import PROFILES
from .generation import Generator
from .retrieval import Retriever
from .conversation import retrieval_question, validate_history
from .guardrails import is_instruction_attack, validate_question
from .generation import GenerationResult, ESCALATE
from .dialogue import conversational_reply
from .relevance import focused_passages


def ask(question, *, audience, store=None, generator=None, offline=False, top_k=3, as_of=None,
        timeout_seconds=None, history=None):
    validate_question(question)
    history = validate_history(history)
    if is_instruction_attack(question) or any(is_instruction_attack(item["content"]) for item in history):
        return GenerationResult("insufficient_evidence", "rules", next_step=ESCALATE)
    reply = conversational_reply(question, audience)
    if reply is not None:
        return reply
    query = retrieval_question(question, history)
    started = time.monotonic()
    try:
        passages = Retriever(store).retrieve(query, audience=audience, top_k=top_k, as_of=as_of,
                                             timeout_seconds=timeout_seconds)
    finally:
        logging.getLogger(__name__).info("RAG stage=search elapsed_ms=%d", (time.monotonic() - started) * 1000)
    passages = focused_passages(query, passages)
    generator = generator or Generator()
    generator.history = history
    result = generator.generate(question, passages, audience=audience, offline=offline)
    if (audience == PROFILES["intern-platform"] and result.status in ("answered", "sources_found")
            and passages and passages[0].rule_key == "jira.bud.read" and not passages[0].stale
            and any(p.document_id == "jira-access" and p.rule_key == "jira.bud.read" and not p.stale
                    for p in result.sources.values())):
        return replace(result, action="preview_jira_access")
    return result
