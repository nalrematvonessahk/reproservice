# controller

API и жизненный цикл запусков; владелец Tamerlan.

Реализовано (Go, 14 ноября 2026): HTTP API (`/api/scenarios`, `/api/runs`, события, cancel, reduce, export), состояние запуска VALIDATING→RESETTING→READY→RUNNING→ASSERTING→COMPLETED с CANCELLED/INFRA_ERROR, Postgres metadata store (`src/controller/store`), идемпотентность по `(scenario_id, idempotency_key)`, 409 при занятом стенде. Проверено end-to-end на реальном docker-compose стенде: сценарий `config/scenario.example.json` воспроизводит двойной платёж, `POST /reduce` подтверждает минимальный набор вмешательств. Интеграционные тесты: `tests/integration/integration_test.go` (`go test -tags=integration ./tests/integration/...`).

Не реализовано/не проверено: TIMEOUT/INAPPLICABLE классификация выхода (оракул различает только PASS/ORIGINAL_FAILURE/OTHER_FAILURE на практике; INAPPLICABLE задан в схеме, но не возникает ни в одном текущем демо-сценарии), полный шестисценарный сравнительный эксперимент из tests/EVALUATION.md.

Контракт: docs/07-architecture.md. Приёмка: docs/06-requirements.md. UX-прототип находится отдельно в prototype/.
