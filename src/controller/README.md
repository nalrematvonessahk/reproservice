# controller

API и жизненный цикл запусков; владелец Tamerlan.

Реализовано (Go, 14 ноября 2026): HTTP API (`/api/scenarios`, `/api/runs`, события, cancel, reduce, export), состояние запуска VALIDATING→RESETTING→READY→RUNNING→ASSERTING→COMPLETED с CANCELLED/INFRA_ERROR, Postgres metadata store (`src/controller/store`), идемпотентность по `(scenario_id, idempotency_key)`, 409 при занятом стенде. Проверено end-to-end на реальном docker-compose стенде: сценарий `config/scenario.example.json` воспроизводит двойной платёж, `POST /reduce` подтверждает минимальный набор вмешательств. Интеграционные тесты: `tests/integration/integration_test.go` (`go test -tags=integration ./tests/integration/...`).

Обновление 18 ноября 2026 (DEMO-52, подробности и результаты: VERIFICATION.md):

- Классификация исхода (`classify` в `orchestrator.go`), приоритет: TIMEOUT > INAPPLICABLE > ORIGINAL_FAILURE > OTHER_FAILURE > PASS. TIMEOUT — checkout не уложился в 15 с (контекст принадлежит контроллеру; orders передаёт контекст запроса, поэтому прерванный checkout не продолжает повторы), даже если дублирующий платёж успел пройти. INAPPLICABLE — любое активное вмешательство сработало не ровно 1 раз (счётчики возвращает proxy в ответе `DELETE /admin/runs/{id}/activate`, поле `fired`). ORIGINAL_FAILURE — только дубль платежа; отсутствие платежа — OTHER_FAILURE. Ошибка Reset или ошибка oracle — INFRA_ERROR, а не вердикт.
- `POST /api/runs/{id}/reduce` (синхронный): алгоритмы `ddmin`, `sequential`, `dependency_aware`, по умолчанию `ddmin`; неизвестный алгоритм — 422 `UNKNOWN_ALGORITHM`; занятый стенд — 409. Ответ: `reduction_id, run_id, state, accepted_intervention_ids, confirmed, one_minimal, unstable, runs_used, max_runs, max_seconds, confirmation_runs, cache_hits, skipped_invalid, stop_reason, error?, duration_ms`. Бюджет сценария ограничивается 100 запусками / 10 минутами. Повторное сокращение того же run допустимо (ID кандидатов `<run>-<reduction>-c<N>`).
- `GET /api/reductions/{id}` — сохранённая запись сокращения (таблица `reductions`; создаётся в `deploy/sql/metadata_schema.sql` и идемпотентно в `store.EnsureSchema` при старте). Неизвестный ID — 404 `REDUCTION_NOT_FOUND`.
- `POST /api/runs/{id}/cancel`: если run или его сокращение выполняется — `{"status":"cancel_requested"}`; run получает CANCELLED (не PASS), сокращение завершается со `stop_reason` `CANCELLED` и последним подтверждённым набором, родительский run не меняется. Если run уже завершён — 200 `{"status":"not_running","state":...}`, результат не переписывается.

Покрытие: пакетные тесты контроллера (`orchestrator_test.go`, 23 теста, in-memory фейки хранилища/runner/oracle/proxy/SUT) и интеграционные `tests/integration/acceptance_test.go`. TIMEOUT проверен только юнит-тестами, на живом стенде не вызывался (демо-клиент orders обрывает вызов через 3 с → 502 → OTHER_FAILURE).

Не реализовано/не проверено: полный шестисценарный сравнительный эксперимент из tests/EVALUATION.md; одновременно выполняется один запуск (один процесс); колонки `candidates.accepted` и `candidates.confirmations` по-прежнему пишутся как false/0 (авторитетная запись — строка `reductions`), `runs.runs_consumed` не используется.

Контракт: docs/07-architecture.md. Приёмка: docs/06-requirements.md. UX-прототип находится отдельно в prototype/.
