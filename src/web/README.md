# web

React UI; владелец Askhat.

Реализовано (React 18 + TypeScript + Vite 4, 14 ноября 2026): `src/web` — конфигурация сценария (встроенные примеры из `config/scenario.example.json` и `config/scenarios/multi-fault-demo.json`), запуск и отслеживание состояния (поллинг), таймлайн событий с parent_event_id (forwarded→dropped→повторный forwarded), сокращение (ddmin/sequential/dependency_aware) с реальными `accepted_intervention_ids`/`runs_used`/`stop_reason`, сравнение версий, экспорт JSON. Говорит напрямую с контроллером на `http://127.0.0.1:7000`, без мока и без сервера — CORS для localhost настроен на стороне controller.

Запуск: `cd src/web && npm install && npm run dev` (нужен Node 16+; версии зависимостей — Vite 4.x/React 18 — выбраны совместимыми с Node 16.20.2 из этой среды). Сборка: `npm run build`.

Проверено реальным запуском против живого стенда: основной путь (сценарий → запуск → ORIGINAL_FAILURE → таймлайн → reduce → export), а также нехэппи-пути (невалидный сценарий → видимая ошибка с correlation_id; STAND_BUSY 409). В процессе проверки найдена и исправлена реальная ошибка контракта: `POST /api/runs` не различал отсутствующее поле `active_intervention_ids` (= все вмешательства) и явный пустой список `[]` (= ноль активных вмешательств, нужно для базовой/исправленной версии в FR11) — исправлено в `src/controller/api.go`, покрыто тестом `tests/integration/integration_test.go:TestZeroActiveInterventions_RunsCleanBaseline`.

Не проверено: мобильная раскладка 390px и реальный UX-пилот с участниками (docs/08-ux.md) — визуально не тестировалось живыми пользователями.

Контракт: docs/07-architecture.md. Приёмка: docs/06-requirements.md. UX-прототип находится отдельно в prototype/.
