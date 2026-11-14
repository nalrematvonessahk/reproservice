# reducer

Baseline ddmin и dependency-aware вариант; владелец Aldiyar.

Реализовано (Go, 14 ноября 2026): `src/reducer` — классический ddmin (Zeller) на множестве ID вмешательств, последовательное удаление (baseline), dependency-aware вариант (та же логика ddmin, кандидаты упорядочены по `LeafFirstOrder`, см. ADR-005: только порядок, не критерий принятия). Общий бюджет (запуски + секунды), подтверждение принятого кандидата 3/3, пометка `unstable` при флуктуации. Модуль не зависит от Docker/HTTP/Postgres — алгоритмы покрыты unit-тестами с фейковым Executor (`src/reducer/reducer_test.go`) и отдельно проверены на реальном стенде через `POST /api/runs/{id}/reduce` (см. tests/integration).

Не выполнено: полный протокол из tests/EVALUATION.md (6 сценариев × 10 прогонов × оба алгоритма с Wilson CI) — это отдельная исследовательская задача с фиксацией характеристик машины, а не инженерная реализация; результаты туда не вписаны (tests/results.csv остаётся только с заголовком).

Контракт: docs/07-architecture.md. Приёмка: docs/06-requirements.md. UX-прототип находится отдельно в prototype/.
