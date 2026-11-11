FROM python:3.11.14-slim-bookworm AS base
WORKDIR /app
COPY rag rag
COPY knowledge knowledge
ENV PYTHONDONTWRITEBYTECODE=1 RAG_DB_PATH=/data/ai-buddy.sqlite3
RUN mkdir /data && python -c "import sqlite3; sqlite3.connect(':memory:').execute('CREATE VIRTUAL TABLE t USING fts5(text)')"
ENTRYPOINT ["python", "-m", "rag"]
CMD ["--help"]

FROM base AS pdf
COPY requirements-pdf.txt .
RUN apt-get update && apt-get install -y --no-install-recommends tesseract-ocr tesseract-ocr-rus \
    && rm -rf /var/lib/apt/lists/* \
    && pip install --no-cache-dir -r requirements-pdf.txt

FROM base AS demo
