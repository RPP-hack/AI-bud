# Воспроизводимое покрытие строк

Отчёт сдаваемой версии: [сводка](coverage/SUMMARY.md), [JSON с перечнем файлов](coverage/summary.json), [Go Cobertura](coverage/backend.xml), [Python Cobertura](coverage/python.xml), [frontend LCOV](frontend-coverage.info).

Считаются исполняемые строки прикладного кода Go, Python и React/TypeScript, включая адаптеры, CLI, имитацию сервиса и точки входа. Тесты, зависимости и сгенерированный protobuf исключены. Это покрытие исполняемых строк, а не доля всех физических строк, включая комментарии и пустые строки.

Go измеряется `go test -coverprofile` и переводится в строки конвертером `gocover-cobertura v1.5.0` с `-ignore-gen-files -ignore-non-code-lines`. Python измеряется coverage.py, frontend — Vitest V8. Исходные форматы и различия инструментов сохранены; statement coverage Go не подменяет строковый отчёт. Итог считается как сумма покрытых строк / сумма исполняемых строк. Порог — строго больше 80%.

## Запуск

Нужны Go из `backend/services/api/go.mod`, Python 3.11+, Node 22.12+, Docker Compose. Python-зависимости зафиксированы в `rag/pyproject.toml`, Node — в `frontend/package-lock.json`.

Из корня репозитория подготовьте окружение:

```sh
python3 -m venv .venv
. .venv/bin/activate
python -m pip install -e './rag[server,dev,pdf]' coverage==7.17.3
npm --prefix frontend ci --no-audit --no-fund
docker compose -f docker-compose.dev.yaml up -d rag-db
export TEST_DATABASE_URL='postgresql://buddy:buddy-local@localhost:55432/buddy'
export RAG_TEST_DATABASE_URL="$TEST_DATABASE_URL"
python scripts/check_release.py
```

Интеграционные тесты создают отдельные временные БД и удаляют их после проверки. Соединение должно разрешать `CREATEDB`. Не задавайте production-соединение.

Скрипт проверяет формат Go, vet, race, интеграции PostgreSQL/pgvector, Python, REST/gRPC, frontend, сборку и 30 сценариев evals. Для тестов векторного хранилища используются фиксированные векторы; модель BGE и ключ OpenRouter этому прогону не нужны. Качество реальных эмбеддингов и доступность внешней LLM проверяются отдельным запуском полного приложения. PDF/OCR-проверка может быть пропущена при отсутствии Tesseract; это явно отражается в результатах тестов.

Тот же прогон настроен в `.github/workflows/release-quality.yml`. Отчёты сохраняются артефактом. Файлы в `docs/coverage/` — снимок локально проверенной версии, их можно воспроизвести приведённой командой.
