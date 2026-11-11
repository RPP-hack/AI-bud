"""Index the documents listed in a trusted, access-controlled manifest."""

import argparse
from dataclasses import asdict
from datetime import date
from hashlib import sha256
import json
from pathlib import Path
from urllib.parse import urlsplit

from ..storage import ROLES, SOURCE_TYPES, SQLiteStore
from .chunking import chunk_pages
from .loaders import load_document

DEMO_MANIFEST = Path(__file__).resolve().parents[2] / "knowledge" / "manifest.json"
_REQUIRED = {"id", "path", "title", "source_type", "origin", "team", "roles"}
_OPTIONAL = {"review_by", "source_url", "rule_key", "rule_value"}
INDEX_VERSION = 1  # bump when normalization changes so the FTS rows are rebuilt


def _metadata(entry):
    if not isinstance(entry, dict) or not _REQUIRED <= entry.keys() <= _REQUIRED | _OPTIONAL:
        raise ValueError("Invalid manifest fields; every document needs an explicit audience and origin")
    for key in entry.keys() - {"roles"}:
        if not isinstance(entry[key], str) or not entry[key].strip() or entry[key] != entry[key].strip():
            raise ValueError(f"Invalid manifest value: {key}")
    roles = entry["roles"]
    if not isinstance(roles, list) or not roles or any(role not in ROLES for role in roles):
        raise ValueError("Invalid or empty document roles")
    if entry["source_type"] not in SOURCE_TYPES or entry["origin"] not in ("demo", "public"):
        raise ValueError("Unknown document type or origin")
    if ("rule_key" in entry) != ("rule_value" in entry):
        raise ValueError("Provide both rule_key and rule_value")
    if "review_by" in entry:
        if date.fromisoformat(entry["review_by"]).isoformat() != entry["review_by"]:
            raise ValueError("review_by must be YYYY-MM-DD")
    if "source_url" in entry:
        url = urlsplit(entry["source_url"])
        if url.scheme not in ("http", "https") or not url.hostname:
            raise ValueError("Expected an HTTP(S) source URL")
    return {**dict.fromkeys(_OPTIONAL), **entry, "roles": sorted(set(roles))}


def index_manifest(path, store, *, ocr=True, chunk_size=1400, chunk_overlap=160):
    manifest = Path(path).resolve(strict=True)
    entries = json.loads(manifest.read_text(encoding="utf-8"))
    if not isinstance(entries, list):
        raise ValueError("The manifest must be a JSON array")
    prepared, ids = [], set()
    for entry in entries:
        meta = _metadata(entry)
        if meta["id"] in ids:
            raise ValueError(f"Duplicate document id: {meta['id']}")
        ids.add(meta["id"])
        source = (manifest.parent / meta.pop("path")).resolve(strict=True)
        if not source.is_relative_to(manifest.parent):
            raise ValueError("Source must be inside the manifest directory")
        meta["source_path"] = str(source.relative_to(manifest.parent))
        chunks = chunk_pages(load_document(source, ocr=ocr),
                             chunk_size=chunk_size, chunk_overlap=chunk_overlap)
        if not chunks:
            raise ValueError(f"No searchable passages in {source.name}")
        content = [asdict(chunk) for chunk in chunks]
        identity = json.dumps([INDEX_VERSION, meta, content], sort_keys=True, ensure_ascii=False)
        prepared.append((meta, sha256(identity.encode()).hexdigest(), chunks))
    store.publish(prepared)
    return {meta["id"]: len(chunks) for meta, _, chunks in prepared}


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("manifest", type=Path, nargs="?", default=DEMO_MANIFEST)
    parser.add_argument("--no-ocr", action="store_true")
    args = parser.parse_args(argv)
    indexed = index_manifest(args.manifest, SQLiteStore(), ocr=not args.no_ocr)
    print(f"Проиндексировано документов: {len(indexed)}, фрагментов: {sum(indexed.values())}")


if __name__ == "__main__":
    main()
