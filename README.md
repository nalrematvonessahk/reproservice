# ReproService — подготовка дипломного проекта

**Разработка платформы воспроизводимого тестирования микросервисов с управлением взаимодействиями и сокращением сценариев сбоя.**

EN: Development of a Reproducible Testing Platform for Microservices with Interaction Management and Failure Scenario Reduction.

KZ: Өзара әрекеттесуді басқару және ақау сценарийлерін қысқарту арқылы микросервистерді қайталанатын тестілеу платформасын әзірлеу.

SE-2416 · Software Engineering · Astana IT University

Tamerlan Khassenov · Aldiyar Gaisa · Askhat Rakhmetollayev

Руководитель: Arailym Orazova, Senior-Lecturer.

## Начать здесь

Откройте **START_HERE.html**: материалы доступны без установки. Интерактивный макет: **prototype/index.html**. Презентация: **presentation/Predefense_1.pptx**.

Подготовлено 14 октября 2026 к контрольной дате 24 октября 2026. Это пакет проектирования и подготовки, не готовая платформа. Материалы подготовлены при помощи ИИ и требуют проверки авторами. Интервью, отзыв руководителя, реальные эксперименты и индивидуальная история Git не выдуманы и пока не представлены.

## Что обновлено после проверки источников

Проверены ключевые разделы полных текстов Reynard, Filibuster и DEMi. Уточнены ограничения и эксперимент; добавлена проверка темы по 291 записи в списке преподавателей. Подробности: docs/14-source-audit.md, docs/15-reducer-decision.md, docs/16-topic-check.md и tests/POST_COMMIT_EXPERIMENT.md.

## Краткое описание для репозитория

Проблема: частичный сбой HTTP-взаимодействия может оставить выполненную операцию без доставленного ответа, а повтор запроса — нарушить бизнес-инвариант. Пользователи: backend- и QA-инженеры. Ожидаемый результат: локальная платформа сохранения, повторения и сокращения поддерживаемых сценариев с экспортом доказательств. Scope: изолированный HTTP/JSON-стенд; без production, Kubernetes и полного детерминизма ОС.

Выбранные технологии: Go для proxy/controller, React + TypeScript для UI, PostgreSQL для метаданных и отдельной тестовой БД, Docker Compose для стенда. Реализация пока не выполнена. Обоснование альтернатив: docs/05-technology-comparison.md.

## Навигация

- [01-proposal.md](docs/01-proposal.md)
- [02-user-research.md](docs/02-user-research.md)
- [03-literature-review.md](docs/03-literature-review.md)
- [04-system-comparison.md](docs/04-system-comparison.md)
- [05-technology-comparison.md](docs/05-technology-comparison.md)
- [06-requirements.md](docs/06-requirements.md)
- [07-architecture.md](docs/07-architecture.md)
- [08-ux.md](docs/08-ux.md)
- [09-team-plan.md](docs/09-team-plan.md)
- [10-supervisor-review.md](docs/10-supervisor-review.md)
- [11-readiness.md](docs/11-readiness.md)
- [12-references.md](docs/12-references.md)
- [13-git-youtrack.md](docs/13-git-youtrack.md)

## Внешние ссылки

Repository URL: **ожидает создания и подключения**.
YouTrack URL: **ожидает создания и подключения**.
Supervisor feedback: **ожидается**.

## Проверка комплекта

Python 3: `python scripts/validate_package.py`. Это проверяет структуру и формат файлов, а не работу будущей платформы. Макет можно открыть напрямую; локальный HTTP-сервер не обязателен. Источники — docs/references.json и docs/12-references.md.

## Единый отчёт к первой предзащите

[Word](docs/ReproService_Report.docx) · [PDF](docs/ReproService_Report.pdf). 34 страницы, подготовлено 14 октября 2026. Внешние подтверждения остаются открытыми; см. разделы 2, 11 и 12 отчёта.

## English report

[Word](docs/ReproService_Report_EN.docx) · [PDF](docs/ReproService_Report_EN.pdf). English report with translated diagrams, 26 pages. Use this version for English-language review. The other preparation files remain in their original language.
