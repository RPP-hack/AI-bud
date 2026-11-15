FROM python:3.11.14-slim-bookworm AS base
WORKDIR /app
ENV PYTHONDONTWRITEBYTECODE=1 RAG_DB_PATH=/data/ai-buddy.sqlite3
COPY pyproject.toml README.md ./
COPY src/ src/
COPY knowledge/ knowledge/
RUN mkdir /data && python -c "import sqlite3; sqlite3.connect(':memory:').execute('CREATE VIRTUAL TABLE t USING fts5(text)')"
ENTRYPOINT ["python", "-m", "rag"]
CMD ["--help"]

FROM base AS pdf
RUN apt-get update && apt-get install -y --no-install-recommends tesseract-ocr tesseract-ocr-eng tesseract-ocr-rus \
    && rm -rf /var/lib/apt/lists/* \
    && python -m pip install --no-cache-dir '.[pdf]'

FROM base AS demo
RUN python -m pip install --no-cache-dir .

FROM base AS server
RUN python -m pip install --no-cache-dir '.[server]'
ENTRYPOINT ["python", "-m", "rag.transport.server"]
CMD []

# Preserve the existing CLI as the default build target.
FROM demo AS default
