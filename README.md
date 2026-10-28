# Согласование отпуска

Тестовое задание
Go + Gin, PostgreSQL 18, Camunda 8,
Keycloak 26.8, Kafka 4.4 и ClickHouse 26
Фронт -  Next.js + React с js

## Запуск
docker compose up
Нужны порты 3000, 8080–8082, 8123

| Сервис | URL | Вход |
| --- | --- | --- |
| Фронт | http://localhost:3000 | `employee / employee`, `employee2 / employee2`, `manager / manager` |
| Swagger UI | http://localhost:8080/swagger/ | Authorize: access token |
| OpenAPI | http://localhost:8080/openapi.yaml |  |
| Camunda Tasklist | http://localhost:8082/tasklist/ | `manager / manager` |
| Camunda Operate | http://localhost:8082/operate/ | `manager / manager` |
| Keycloak admin | http://localhost:8081 | `admin / admin` |
| ClickHouse HTTP | http://localhost:8123 | `vacation / vacation` |

Использовать localhost, он в редиректах указан и в issuer. Хранится в docker volumes


| Баланс | Дней | Руководитель | Результат DMN |
| --- | --- | --- | --- |
| Не хватает | Любое | Не нужен | `rejected` |
| Хватает | 1-2 | Не нужен | `auto_approved` |
| Хватает | > 2 | Отклонил | `rejected` |
| Хватает | 3-14 | Одобрил | `approved` |
| Хватает | > 14 | Одобрил | `needs_hr_review` |


Бекэнд разделен согласно требованиям

## API и доступ

| Метод | Путь | Роль |
| --- | --- | --- |
| GET | `/api/me` | employee |
| POST | `/api/vacation-requests` | employee |
| GET | `/api/vacation-requests?limit=50&offset=0` | employee, только свои |
| GET | `/api/manager/vacation-requests?limit=50&offset=0` | manager, все |
| GET | `/healthz`, `/readyz` | без токена |



Redis добавлять не стал, точный баланс всё равно проверяется в транзакции PostgreSQL,
  а кэш добавит инвалидацию и ещё один сервис