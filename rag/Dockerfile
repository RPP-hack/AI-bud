FROM python:3.11.14-slim-bookworm AS base
WORKDIR /app
ENV PYTHONDONTWRITEBYTECODE=1 HF_HOME=/models TOKENIZERS_PARALLELISM=false
RUN python -m pip install --no-cache-dir --index-url https://download.pytorch.org/whl/cpu 'torch>=2.6,<3'
COPY pyproject.toml README.md ./
COPY src/ src/
COPY knowledge/ knowledge/
ENTRYPOINT ["python", "-m", "rag"]
CMD ["--help"]

FROM base AS pdf
RUN apt-get update && apt-get install -y --no-install-recommends tesseract-ocr tesseract-ocr-eng tesseract-ocr-rus \
    && rm -rf /var/lib/apt/lists/* \
    && python -m pip install --no-cache-dir '.[pdf,local]'

FROM base AS demo
RUN python -m pip install --no-cache-dir '.[local]'

FROM base AS server
RUN python -m pip install --no-cache-dir '.[server,local]'
ENTRYPOINT ["python", "-m", "rag.transport.server"]
CMD []

# Preserve the existing CLI as the default build target.
FROM demo AS default
