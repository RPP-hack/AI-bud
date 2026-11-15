"""Transport-independent, read-only RAG use cases and public result projections."""

from contextlib import contextmanager
from dataclasses import asdict, dataclass, replace
import logging
import math
import re
import sqlite3
import threading
import time
import uuid

from .generation import GenerationConfig, GenerationError, Generator, GuardrailError
from .pipeline import ask
from .retrieval import Retriever
from .storage import Audience, SQLiteStore
from .guardrails import validate_question

logger = logging.getLogger(__name__)


class ApplicationError(Exception):
    def __init__(self, code):
        self.code = code
        super().__init__(code)


@dataclass(frozen=True)
class Actor:
    user_id: str
    role: str
    team: str


def source_view(chunk):
    # Deliberately exclude filesystem paths, ACLs and internal rule metadata.
    return {name: getattr(chunk, name) for name in (
        "document_id", "version_id", "chunk_id", "title", "text", "source_url",
        "page_number", "origin", "stale", "review_by", "start_char", "end_char",
    )}


class RagApplication:
    def __init__(self, store=None, *, concurrency=16, timeout_seconds=30, generator_factory=None):
        if concurrency < 1 or not math.isfinite(timeout_seconds) or timeout_seconds <= 0:
            raise ValueError("Invalid RAG application limits")
        self.store = store if store is not None else SQLiteStore()
        self.timeout_seconds = timeout_seconds
        self._slots = threading.BoundedSemaphore(concurrency)
        self._generator_factory = generator_factory or self._generator

    @staticmethod
    def _generator(timeout_seconds):
        try:
            config = GenerationConfig.from_env()
        except ValueError:
            raise ApplicationError("temporarily_unavailable") from None
        return Generator(replace(config, timeout_seconds=min(25, timeout_seconds)))

    def ready(self):
        try:
            with self.store.connect(read_only=True) as db:
                return (db.execute("SELECT count(*) FROM documents WHERE current=1").fetchone()[0] > 0
                        and db.execute("SELECT count(*) FROM passages").fetchone()[0] > 0)
        except (sqlite3.Error, OSError):
            return False

    @staticmethod
    def _validate(text, actor, top_k, request_id):
        try:
            validate_question(text)
        except ValueError:
            raise ApplicationError("invalid_input") from None
        if type(top_k) is not int or not 1 <= top_k <= 50:
            raise ApplicationError("invalid_input")
        if not isinstance(actor, Actor) or any(
            not isinstance(v, str) or not v.strip() or len(v) > 128
            or any(ord(c) < 32 or 0xD800 <= ord(c) <= 0xDFFF for c in v)
            for v in (actor.user_id, actor.role, actor.team)
        ):
            raise ApplicationError("invalid_input")
        try:
            audience = Audience(actor.role, actor.team)
        except ValueError:
            raise ApplicationError("invalid_input") from None
        if not isinstance(request_id, str) or (request_id and not re.fullmatch(r"[A-Za-z0-9_.:-]{1,128}", request_id)):
            raise ApplicationError("invalid_input")
        return audience, request_id or str(uuid.uuid4())

    @contextmanager
    def _attempt(self, timeout_seconds):
        timeout = self.timeout_seconds if timeout_seconds is None else min(timeout_seconds, self.timeout_seconds)
        if not math.isfinite(timeout) or timeout <= 0:
            raise ApplicationError("deadline_exceeded")
        if not self._slots.acquire(blocking=False):
            raise ApplicationError("resource_exhausted")
        deadline = time.monotonic() + timeout
        try:
            yield lambda: max(0, deadline - time.monotonic())
            if time.monotonic() >= deadline:
                raise ApplicationError("deadline_exceeded")
        except GuardrailError as exc:
            logger.warning("RAG output rejected (%s)", exc.code)
            if time.monotonic() >= deadline:
                raise ApplicationError("deadline_exceeded") from None
            raise ApplicationError("temporarily_unavailable") from None
        except (sqlite3.Error, OSError, GenerationError):
            if time.monotonic() >= deadline:
                raise ApplicationError("deadline_exceeded") from None
            logger.warning("RAG dependency failed")
            raise ApplicationError("temporarily_unavailable") from None
        finally:
            self._slots.release()

    def answer(self, question, actor, *, request_id="", mode="offline", top_k=3, timeout_seconds=None):
        audience, request_id = self._validate(question, actor, top_k, request_id)
        if mode not in ("offline", "llm"):
            raise ApplicationError("invalid_input")
        with self._attempt(timeout_seconds) as remaining:
            # Lazily initialize the provider only when the pipeline needs it.
            factory = self._generator_factory

            class DeadlineGenerator(Generator):
                def _complete(self, question, audience, fresh):
                    timeout = remaining()
                    if timeout <= 0:
                        raise ApplicationError("deadline_exceeded")
                    return factory(timeout)._complete(question, audience, fresh)

            result = ask(question, audience=audience, store=self.store,
                         generator=DeadlineGenerator(), offline=mode == "offline", top_k=top_k)
            response = dict(request_id=request_id, status=result.status, mode=result.mode,
                            steps=[asdict(step) for step in result.steps],
                            sources={key: source_view(chunk) for key, chunk in result.sources.items()},
                            next_step=result.next_step, model=result.model, action=result.action)
        return response

    def search(self, query, actor, *, request_id="", top_k=3, timeout_seconds=None):
        audience, request_id = self._validate(query, actor, top_k, request_id)
        with self._attempt(timeout_seconds):
            hits = Retriever(self.store).retrieve(query, audience=audience, top_k=top_k)
            response = dict(request_id=request_id, sources=[source_view(chunk) for chunk in hits])
        return response
