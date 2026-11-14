# proxy

HTTP interception и scheduler; владелец Tamerlan.

Реализовано (Go, 14 ноября 2026): маршрутизация `/{target}/...` между демо-сервисами orders/payments/inventory, сопоставление по Selector (logical_operation+source+target+method+normalized_path+attempt), действия `delay`, `http_error` (синтез до upstream) и `drop_response_after_success` (upstream завершается, событие логируется, затем соединение с вызывающим обрывается — реализовано через hijack соединения). Редактирование Authorization/Cookie/Set-Cookie перед логированием. Admin API (`/admin/runs/{run_id}/activate|deactivate`) ограничивает действие вмешательств текущим активным запуском. Проверено реальным прогоном: события `forwarded`→`dropped`(parent_event_id)→повторный `forwarded` видны в `GET /api/runs/{id}/events`.

Контракт: docs/07-architecture.md. Приёмка: docs/06-requirements.md. UX-прототип находится отдельно в prototype/.
