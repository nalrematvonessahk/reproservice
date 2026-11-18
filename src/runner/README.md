# runner

Reset и oracle; владелец Aldiyar.

Реализовано (Go, 14 ноября 2026): `src/runner` — сброс тестовой БД (TRUNCATE + фиксированный seed из `deploy/sql/seed.sql`), проверка хеша seed, health checks перед READY. `src/oracle` — проверка инварианта `payment_exactly_once` (COUNT платежей со статусом succeeded по order_id); неизвестный invariant_id или ошибка запроса возвращаются как infra-ошибка, а не как нарушение (FR06). Упрощение по сравнению с архитектурным черновиком: сброс не пересоздаёт Docker-volume, а восстанавливает состояние через SQL-транзакцию — тот же наблюдаемый эффект (чистое состояние перед каждым кандидатом), но без управления Docker-сокетом из этого компонента.

Обновление 18 ноября 2026 (DEMO-52, результаты: VERIFICATION.md):

- Runner: хеш seed-файла сверяется перед каждым `Reset` (изменённый файл блокирует запуск); после коммита число строк в `orders`, `payments`, `inventory_reservations` сверяется с состоянием внутри транзакции; затем health checks. Любая ошибка `Reset` прерывает запуск до активации proxy и checkout и даёт INFRA_ERROR, а не вердикт о приложении (FR02). Отдельного автотеста на подмену seed-файла и на сверку числа строк нет; поведение проверено чтением кода и успешными прогонами. Вживую проверено, что состояние прошлого запуска очищается (`TestReset_ClearsPreviousRunState`).
- Ограничения: `deploy/sql/seed.sql` намеренно пуст, поэтому «восстановление seed» означает «три таблицы очищены»; `seed_hash` в сценариях — заглушка `TO_BE_COMPUTED` и не сравнивается с `SeedHash()` (FR07 вне объёма). Сброс — SQL-truncate, не пересоздание volume.
- Oracle (`payment_exactly_once`): чистая функция `EvaluatePaymentExactlyOnce(count)`. Нарушение при count != 1; `Kind=duplicate` только при count > 1 (контроллер даёт ORIGINAL_FAILURE), `Kind=missing` при count = 0 (OTHER_FAILURE). Раньше платёж, не созданный вовсе, ошибочно считался ORIGINAL_FAILURE. Неизвестный invariant_id или ошибка запроса — infra-ошибка, не нарушение. Итоговый исход (приоритет TIMEOUT > INAPPLICABLE > ORIGINAL_FAILURE > OTHER_FAILURE > PASS) определяет контроллер, см. src/controller/README.md. Юнит-тесты oracle: 2.

Контракт: docs/07-architecture.md. Приёмка: docs/06-requirements.md. UX-прототип находится отдельно в prototype/.
