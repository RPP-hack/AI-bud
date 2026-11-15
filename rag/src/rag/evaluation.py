"""Reproducible regression evaluations and separately bound human answer reviews."""

import argparse
from collections import defaultdict
from dataclasses import asdict
from datetime import date
from hashlib import sha256
import json
from pathlib import Path
import tempfile
import time

from .access import PROFILES
from .application import source_view
from .generation import ChatCompletionsClient, GenerationConfig, Generator, SYSTEM_PROMPT, _source_record
from .guardrails import POLICY_VERSION
from .ingestion.indexing import DEMO_MANIFEST, index_manifest
from .pipeline import ask
from .retrieval import Retriever
from .storage import SQLiteStore

REVIEW_CRITERIA = ("grounded", "relevant", "complete", "safe")
STATUSES = {"answered", "sources_found", "insufficient_evidence", "conflict", "needs_review"}


def digest(value):
    return sha256(json.dumps(value, ensure_ascii=True, sort_keys=True).encode()).hexdigest()


def load_suite(path):
    suite = json.loads(Path(path).read_text(encoding="utf-8"))
    if not isinstance(suite, dict) or suite.get("version") != 1 or not suite.get("cases"):
        raise ValueError("Expected a nonempty version 1 evaluation suite")
    date.fromisoformat(suite["as_of"])
    seen = set()
    required = {"id", "requirement", "category", "profile", "question", "documents",
                "offline_status", "llm_statuses", "action", "rubric"}
    for case in suite["cases"]:
        if not isinstance(case, dict) or not required <= case.keys() <= required | {"top_k"}:
            raise ValueError("Invalid case fields")
        if any(not isinstance(case[k], str) or not case[k].strip() for k in
               ("id", "requirement", "category", "profile", "question", "rubric")):
            raise ValueError("Empty or invalid case label")
        if case["id"] in seen or case["profile"] not in PROFILES:
            raise ValueError("Duplicate case ID or unknown profile")
        seen.add(case["id"])
        if (not isinstance(case["documents"], list)
                or any(not isinstance(d, str) or not d for d in case["documents"])
                or len(set(case["documents"])) != len(case["documents"])
                or case["offline_status"] not in STATUSES
                or not isinstance(case["llm_statuses"], list) or not case["llm_statuses"]
                or any(s not in STATUSES for s in case["llm_statuses"])
                or case["action"] not in (None, "preview_jira_access")
                or type(case.get("top_k", 3)) is not int or not 1 <= case.get("top_k", 3) <= 50):
            raise ValueError("Invalid case expectation")
    return suite


class MeasuredClient:
    def __init__(self, config):
        self.client = ChatCompletionsClient(config)
        self.calls = 0
        self.seconds = 0
        self.context = []

    def complete(self, messages):
        self.context = json.loads(messages[1]["content"])["sources"]
        self.calls += 1
        start = time.perf_counter()
        try:
            return self.client.complete(messages)
        finally:
            self.seconds += time.perf_counter() - start


def _answer_digest(meta, row):
    return digest({"run": meta, **{k: row[k] for k in
                  ("id", "question", "profile", "rubric", "result", "error")}})


def run_suite(suite, manifest=DEMO_MANIFEST, *, mode="offline", config=None):
    if mode not in ("offline", "llm"):
        raise ValueError("Unknown evaluation mode")
    if mode == "llm" and config is None:
        config = GenerationConfig.from_env()
    as_of = date.fromisoformat(suite["as_of"])
    client = MeasuredClient(config) if mode == "llm" else None
    generator = Generator(config, client=client)
    rows = []
    with tempfile.TemporaryDirectory(prefix="rag-eval-") as directory:
        store = SQLiteStore(Path(directory) / "eval.sqlite3")
        index_manifest(manifest, store)
        with store.connect(read_only=True) as db:
            versions = [tuple(r) for r in db.execute(
                "SELECT document_id, version_id FROM documents WHERE current=1 ORDER BY document_id")]
        known = {r[0] for r in versions}
        if any(set(c["documents"]) - known for c in suite["cases"]):
            raise ValueError("Expected document is missing from the corpus")
        package = Path(__file__).parent
        meta = dict(version=1, specification=suite["specification"], mode=mode,
                    case_ids=[c["id"] for c in suite["cases"]],
                    as_of=as_of.isoformat(), dataset_sha256=digest(suite), corpus_sha256=digest(versions),
                    prompt_sha256=digest(SYSTEM_PROMPT), policy_version=POLICY_VERSION,
                    implementation_sha256=digest({name: (package / name).read_text() for name in
                        ("evaluation.py", "generation.py", "guardrails.py", "pipeline.py", "retrieval.py",
                         "storage.py", "terms.py", "ingestion/indexing.py", "ingestion/chunking.py")}),
                    model=config.model if mode == "llm" else None,
                    provider=config.base_url if mode == "llm" else None,
                    generation_limits={k: getattr(config, k) for k in
                        ("timeout_seconds", "max_output_tokens", "max_context_chars")} if mode == "llm" else None)
        for case in suite["cases"]:
            audience = PROFILES[case["profile"]]
            top_k = case.get("top_k", 3)
            start = time.perf_counter()
            hits = Retriever(store).retrieve(case["question"], audience=audience, top_k=top_k, as_of=as_of)
            retrieval_ms = (time.perf_counter() - start) * 1000
            documents = list(dict.fromkeys(p.document_id for p in hits))
            expected = set(case["documents"])
            calls_before, seconds_before = (client.calls, client.seconds) if client else (0, 0)
            result, error = None, None
            start = time.perf_counter()
            try:
                result = ask(case["question"], audience=audience, store=store, generator=generator,
                             offline=mode == "offline", top_k=top_k, as_of=as_of)
            except Exception as exc:
                # No provider messages, API keys, stack traces or local paths in artifacts.
                error = type(exc).__name__
            answer_ms = (time.perf_counter() - start) * 1000
            calls = client.calls - calls_before if client else 0
            sources = list(result.sources.values()) if result else []
            visible = lambda p: p.team in ("*", audience.team) and audience.role in p.roles
            hit_ids = {p.citation_id: p for p in hits}
            checks = {
                "retrieval": expected <= set(documents) if expected else not documents,
                "first_source": bool(documents and documents[0] in expected) if expected else True,
                "acl": all(visible(p) for p in hits + sources),
                "status": result is not None and result.status in (
                    [case["offline_status"]] if mode == "offline" else case["llm_statuses"]),
                "action": result is not None and result.action == case["action"],
                "citations": result is not None and all(
                    step.source_ids and all(ref in result.sources for ref in step.source_ids)
                    for step in result.steps) and all(hit_ids.get(p.citation_id) == p for p in sources),
                "abstention": result is not None and (case["offline_status"] != "insufficient_evidence"
                    or (result.status == "insufficient_evidence" and not result.steps and not sources
                        and bool(result.next_step))),
                "escalation": result is not None and (result.status not in (
                    "conflict", "needs_review", "insufficient_evidence") or all(
                        word in result.next_step for word in ("наставник", "руководител", "HRBP"))),
                "local_rules": not (result and result.mode == "rules" and calls),
                "no_side_effects": True,
                "provider_context": True,
            }
            if calls:
                # Compare the entire transmitted source records with authorized hits.
                allowed = [_source_record("", p) for p in hits if visible(p) and not p.stale]
                checks["provider_context"] = all(
                    dict(p, source_id="") in allowed for p in client.context)
            with store.connect(read_only=True) as db:
                checks["no_side_effects"] = db.execute("SELECT count(*) FROM access_requests").fetchone()[0] == 0
            rank = next((i for i, doc in enumerate(documents, 1) if doc in expected), None)
            row = {k: case[k] for k in ("id", "question", "profile", "category", "requirement", "rubric")}
            row.update(checks=checks, passed=all(checks.values()), documents=documents, top_k=top_k,
                       expected_documents=case["documents"],
                       hit_at_1=float(bool(documents and documents[0] in expected)) if expected else None,
                       recall_at_k=len(expected & set(documents)) / len(expected) if expected else None,
                       reciprocal_rank=1 / rank if rank else (0 if expected else None),
                       retrieval_ms=round(retrieval_ms, 3), answer_ms=round(answer_ms, 3),
                       provider_ms=round((client.seconds - seconds_before) * 1000, 3) if client else 0,
                       provider_calls=calls, error=error,
                       result=None if result is None else dict(status=result.status, mode=result.mode,
                           steps=[asdict(step) for step in result.steps], action=result.action,
                           sources={key: source_view(p) for key, p in result.sources.items()},
                           next_step=result.next_step, model=result.model))
            row["answer_sha256"] = _answer_digest(meta, row)
            rows.append(row)
    metrics = {}
    for name in ("hit_at_1", "recall_at_k", "reciprocal_rank"):
        values = [r[name] for r in rows if r[name] is not None]
        metrics[name] = dict(value=sum(values) / len(values) if values else None, denominator=len(values))
    for name in rows[0]["checks"]:
        metrics[name] = dict(value=sum(r["checks"][name] for r in rows) / len(rows), denominator=len(rows))
    slices = defaultdict(lambda: {"total": 0, "passed": 0})
    for row in rows:
        slices[row["category"]]["total"] += 1
        slices[row["category"]]["passed"] += row["passed"]
    return dict(meta=meta, automatic_passed=all(r["passed"] for r in rows),
                semantic_quality="not_evaluated", quality_passed=None,
                metrics=metrics, slices=dict(slices), cases=rows)


def review_template(report):
    return {row["id"]: dict(answer_sha256=row["answer_sha256"], reviewer="", notes="",
                            **dict.fromkeys(REVIEW_CRITERIA)) for row in report["cases"]}


def grade(report, reviews):
    if report["meta"]["mode"] != "llm":
        raise ValueError("Offline excerpts cannot establish LLM answer quality")
    if (not report["cases"] or [r["id"] for r in report["cases"]] != report["meta"]["case_ids"]
            or len(set(report["meta"]["case_ids"])) != len(report["cases"])):
        raise ValueError("The report must retain every evaluated case")
    if set(reviews) != {r["id"] for r in report["cases"]}:
        raise ValueError("Every case must be reviewed exactly once")
    for row in report["cases"]:
        review = reviews[row["id"]]
        if (review.get("answer_sha256") != _answer_digest(report["meta"], row)
                or not isinstance(review.get("reviewer"), str) or not review["reviewer"].strip()
                or any(type(review.get(k)) is not bool for k in REVIEW_CRITERIA)):
            raise ValueError("Incomplete review or review of a different answer")
    report["reviews"] = reviews
    report["semantic_quality"] = "human_reviewed"
    automatic_passed = all(row["checks"] and all(value is True for value in row["checks"].values())
                           and row["error"] is None for row in report["cases"])
    report["quality_passed"] = (automatic_passed and all(
        review[k] for review in reviews.values() for k in REVIEW_CRITERIA))
    return report


def _write(path, data):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(data, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)
    run = sub.add_parser("run")
    run.add_argument("--suite", type=Path, default=Path("rag/evals/cases.json"))
    run.add_argument("--manifest", type=Path, default=DEMO_MANIFEST)
    run.add_argument("--mode", choices=("offline", "llm"), default="offline")
    run.add_argument("--output", type=Path, required=True)
    review = sub.add_parser("grade")
    review.add_argument("report", type=Path)
    review.add_argument("reviews", type=Path)
    review.add_argument("--output", type=Path, required=True)
    args = parser.parse_args(argv)
    try:
        if args.command == "run":
            report = run_suite(load_suite(args.suite), args.manifest, mode=args.mode)
            _write(args.output, report)
            if args.mode == "llm":
                _write(args.output.with_suffix(".review.json"), review_template(report))
            print(f"Automatic gates: {'PASS' if report['automatic_passed'] else 'FAIL'}; "
                  f"{sum(r['passed'] for r in report['cases'])}/{len(report['cases'])} cases; "
                  "semantic quality: not evaluated")
            for row in report["cases"]:
                if not row["passed"]:
                    print(row["id"] + ": " + ", ".join(k for k, v in row["checks"].items() if not v))
            return 0 if report["automatic_passed"] else 1
        report = grade(json.loads(args.report.read_text()), json.loads(args.reviews.read_text()))
        _write(args.output, report)
        print("Reviewed quality: " + ("PASS" if report["quality_passed"] else "FAIL"))
        return 0 if report["quality_passed"] else 1
    except (ValueError, OSError, KeyError, TypeError) as exc:
        parser.exit(2, f"Evaluation configuration error ({type(exc).__name__}); check inputs and provider settings.\n")


if __name__ == "__main__":
    raise SystemExit(main())
