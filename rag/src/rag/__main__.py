"""Local AI-buddy demo. Employee profiles simulate trusted backend sessions."""

import argparse
from dataclasses import asdict
import json
import os
import sqlite3
import sys

from .access import PROFILES, request_access
from .generation import GenerationError
from .embeddings import EmbeddingError
from .ingestion.indexing import main as index
from .pipeline import ask
from .retrieval import Retriever
from .storage import StorageError, create_store

_STATUS = {
    "conversation": "AI-buddy",
    "conflict": "Инструкции расходятся", "insufficient_evidence": "Недостаточно сведений",
    "needs_review": "Нужна актуальная инструкция", "answered": "Ответ модели",
    "sources_found": "Офлайн · выдержки из источников",
}


def main(argv=None):
    args = list(sys.argv[1:] if argv is None else argv)
    parser = argparse.ArgumentParser(prog="python -m rag", description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)
    sub.add_parser("index", help="Опубликовать базу знаний из манифеста")
    if args[:1] == ["index"]:
        try:
            return index(args[1:])
        except (ValueError, OSError, ImportError, sqlite3.Error, RuntimeError) as exc:
            parser.exit(1, f"Ошибка индексации: {exc}\n")
    for cmd in ("ask", "search", "access"):
        tool = sub.add_parser(cmd)
        tool.add_argument("--profile", choices=PROFILES, default="intern-platform")
        if cmd in ("ask", "search"):
            tool.add_argument("question")
            tool.add_argument("--top-k", type=int, default=3)
        if cmd == "ask":
            tool.add_argument("--offline", action="store_true")
            tool.add_argument("--json", action="store_true")
        if cmd == "access":
            tool.add_argument("--confirm", action="store_true", help="Отправить показанную тестовую заявку")
    options = parser.parse_args(args)
    audience = PROFILES[options.profile]
    try:
        store = create_store()
        if options.command == "access":
            output = request_access(store, options.profile, confirm=options.confirm)
            print(json.dumps(output, ensure_ascii=False, indent=2))
            return
        if options.command == "search":
            hits = Retriever(store).retrieve(options.question, audience=audience, top_k=options.top_k)
            print(json.dumps([dict(asdict(p), citation_id=p.citation_id) for p in hits],
                             ensure_ascii=False, indent=2))
            return
        offline = options.offline or not os.environ.get("RAG_LLM_API_KEY")
        if not offline and not options.json:
            print("Ищу инструкции и жду ответа модели…", file=sys.stderr, flush=True)
        result = ask(options.question, audience=audience, store=store,
                     offline=offline, top_k=options.top_k)
        if options.json:
            print(json.dumps(asdict(result), ensure_ascii=False, indent=2))
            return
        print(_STATUS[result.status], result.answer, sep="\n\n")
        for key, p in result.sources.items():
            origin = "учебный пример" if p.origin == "demo" else "открытый источник"
            locator = f", стр. {p.page_number}" if p.page_number else ""
            print(f"[{key}] {p.title} ({origin}{locator}) · {p.source_url or p.source_path}")
        if result.action:
            print(f"Посмотреть тестовую заявку: python -m rag access --profile {options.profile}")
    except (ValueError, GenerationError, EmbeddingError, StorageError, PermissionError, sqlite3.Error) as exc:
        if isinstance(exc, sqlite3.OperationalError) and not store.path.exists():
            parser.exit(1, "Индекс отсутствует. Выполни python -m rag index.\n")
        parser.exit(1, f"AI-buddy: {exc}\n")


if __name__ == "__main__":
    main()
