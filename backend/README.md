# AI-bud

AI-помощник для адаптации стажёров. Сейчас реализован первый срез бэкенда:
предложение заявки на доступ, подтверждение, отмена, выполнение через HTTP-сервис,
проверка статуса и сохранение результата в PostgreSQL. RAG подключён через gRPC;
REST-сценарии и запуск описаны в [RAG API](../docs/rag-api.md). Фронтенд и чат пока не подключены.

Все пользователи, ресурсы, токены и правила в этом прототипе синтетические.
Это не регламенты Авито и не готовая корпоративная система авторизации.

## Быстрый запуск

Нужен работающий Docker Desktop с Docker Compose. Из корня репозитория:

```sh
cd backend
docker compose up --build -d
docker compose ps
curl http://localhost:8080/ready
```

Compose поднимает PostgreSQL, API на `localhost:8080` и внутреннюю имитацию сервиса
заявок. Миграция выполняется при старте под advisory lock. Данные переживают
перезапуск контейнеров в volume `buddy-db`. `docker compose down` останавливает
сервисы и сохраняет volume.

Имитация переводит заявку из `pending` в `granted` при чтении спустя 30 секунд
после создания. Это демонстрационное поведение, а не автоматическая выдача реальных
доступов. Для ручного ожидания задайте `MOCK_GRANT_AFTER: "0s"` в Compose.

## Пройти сценарий без фронтенда

Тестовые аккаунты:

| Bearer token | Пользователь | Команда | Разрешено запросить |
|---|---|---|---|
| `demo-alex` | alex | notifications | `read` к notifications |
| `demo-sam` | sam | payments | `read` к payments |

1. Создать предложение. `Idempotency-Key` обязателен и обозначает одну логическую
операцию пользователя. Повтор с тем же ключом и параметрами возвращает прежнее
предложение. Другое содержимое с тем же ключом даёт `409`.

```sh
curl -sS -X POST http://localhost:8080/api/access-requests \
  -H 'Authorization: Bearer demo-alex' \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: first-access-1' \
  -d '{"resource":"notifications","access":"read","reason":"Первая задача"}'
```

2. Скопировать возвращённый `id` в переменную. Подтвердить версию предложения:

```sh
REQUEST_ID=сюда_id_из_ответа
curl -sS -X POST "http://localhost:8080/api/access-requests/$REQUEST_ID/confirm" \
  -H 'Authorization: Bearer demo-alex' \
  -H 'Content-Type: application/json' \
  -d '{"version":1}'
```

Ответ содержит `state: submitted` и `ticket` с номером и статусом `pending`.
Повторное подтверждение этой операции не создаёт вторую заявку. Разные ключи
предложений обозначают разные операции; бизнес-дедупликации всех заявок на один
ресурс пока нет.

3. Проверить сохранённую запись и прогресс:

```sh
curl -sS "http://localhost:8080/api/access-requests/$REQUEST_ID" -H 'Authorization: Bearer demo-alex'
curl -sS http://localhost:8080/api/onboarding -H 'Authorization: Bearer demo-alex'
```

4. Спустя 30 секунд обновить внешний статус:

```sh
curl -sS -X POST "http://localhost:8080/api/access-requests/$REQUEST_ID/refresh" \
  -H 'Authorization: Bearer demo-alex'
curl -sS http://localhost:8080/api/onboarding -H 'Authorization: Bearer demo-alex'
```

После `granted` прогресс станет `done`, следующий шаг — `start_local_setup`.
GET читает сохранённые данные; `/refresh` явно синхронизирует их с сервисом.
Фоновый worker также обновляет статусы; отключение — `RECONCILE_ENABLED=false`.

До подтверждения предложение можно отменить через `POST .../{id}/cancel`
с телом `{"version":1}`. Чужая заявка возвращает `404`. Запрос `write` или ресурса
другой команды возвращает `403`. Неизвестные поля тела, в том числе `user_id`,
отклоняются. Отправка с потерянным ответом даёт `202` и `unknown`;
вызов `/refresh` проверяет результат и при необходимости повторяет ту же операцию
с тем же ключом. Прерванное состояние `submitting` восстанавливается тем же способом.

## Локальная разработка

Команды этого раздела выполняются из `backend/`.

Версия сборки — Go 1.27.4, PostgreSQL 17.6. Зависимости закреплены в `go.mod`/`go.sum`.
Go-модуль находится в `services`, запуск `go test ./...` из корня не предусмотрен.

```sh
docker compose up -d db
cp .env.example .env
set -a
source .env
set +a
cd services/api
go run ./cmd/mock-access
```

В другом терминале загрузить те же переменные и запустить `go run ./cmd/server`
из `services`. Go-приложения читают окружение, файл `.env` сами не загружают.
Для локального запуска освободите порт 8080, если API уже запущен в Compose.

`DEMO_MODE=true` обязателен: без него сервер отказывается запускаться, поскольку
корпоративный способ входа ещё не реализован. Тестовые токены публичные и пригодны
только для локальной разработки. Порты Compose опубликованы на loopback.

## Проверки

Из каталога `backend/` при установленном Go:

```sh
make test
make vet
export TEST_DATABASE_URL='postgres://buddy:buddy-local@localhost:55432/buddy?sslmode=disable'
make integration
make coverage
```

Интеграционные тесты создают отдельные базы с уникальным префиксом `buddy_test_`
и удаляют только их после завершения. Нужен тестовый пользователь с правом
создания БД. Используйте локальный Compose, не рабочую корпоративную БД.
Без `TEST_DATABASE_URL` запуск с тегом `integration` завершается ошибкой.

Покрытие считается по всему написанному Go-коду, включая entrypoints и имитацию,
но без тестов и внешних зависимостей. `go tool cover` измеряет **операторы**, а не
строки. Текущий результат и ограничения — в `docs`.
Это ещё не отчёт по всему будущему приложению и не замена требуемого в кейсе
измерения строк Go, Python и фронтенда.

## Навигация по проекту

- `docs` — границы модулей, зависимости и принятые решения.
- `specs` — поведение сценария и критерии приёмки.
- `contracts` — текущий HTTP-контракт без привязки к фронтенду.
- `services` — модель заявки и переходы состояний.
- `services` — сценарии использования и порты.
- `services` — PostgreSQL, HTTP-клиент, демо-политика и тестовое хранилище.
- `services` — вычисление прогресса по результату заявки.
- `services` — HTTP, DTO и тестовая аутентификация.
- `services` — имитация внешней системы, отдельный процесс.
- `services` — PostgreSQL и сценарий API → mock → сохранение.
