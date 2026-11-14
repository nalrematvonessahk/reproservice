# runner

Reset и oracle; владелец Aldiyar.

Реализовано (Go, 14 ноября 2026): `src/runner` — сброс тестовой БД (TRUNCATE + фиксированный seed из `deploy/sql/seed.sql`), проверка хеша seed, health checks перед READY. `src/oracle` — проверка инварианта `payment_exactly_once` (COUNT платежей со статусом succeeded по order_id); неизвестный invariant_id или ошибка запроса возвращаются как infra-ошибка, а не как нарушение (FR06). Упрощение по сравнению с архитектурным черновиком: сброс не пересоздаёт Docker-volume, а восстанавливает состояние через SQL-транзакцию — тот же наблюдаемый эффект (чистое состояние перед каждым кандидатом), но без управления Docker-сокетом из этого компонента.

Контракт: docs/07-architecture.md. Приёмка: docs/06-requirements.md. UX-прототип находится отдельно в prototype/.
