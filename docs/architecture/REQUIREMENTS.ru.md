# Требования и проверяемость

Срез OC-2026-11-15-01. **PoC** — согласованная узкая интеграция; **MVP** — внутренний этап без единого утверждённого baseline; **target** — конечное продуктовое решение. «Установлено» относится к предметному требованию, не к готовности кода. Все проверки в таблице, кроме явно отмеченных исполненных, — критерии будущей приёмки. Ни один численный SLO не назначен этим документом.

| ID | Scope | Требование / статус | Проверка или имеющееся свидетельство | Открытый вопрос | Основание |
|---|---|---|---|---|---|
| R-001 | target; PoC | Cache-aside установлен | MISS → приложение читает БД → Put; Get не вызывает БД | Политика повторного заполнения при конкуренции — RFC-001 | E-001 |
| R-002 | target | Единый write-region установлен | Запись из другого региона направляется к текущему writer; проверить отказ маршрута | ACK, fencing и смена primary — RFC-003 | E-002 |
| R-003 | target; PoC RF3 | RF3 установлен, одинаковые конфигурации pod поддержаны | Проверить число членов и размещение, отказ одного peer; PoC killleader исполнен | Физические failure domains и sizing не доказаны | E-003, E-015, E-016 |
| R-004 | PoC | Один регион/шард, 3 RAM storage, static peers + CP/gateway; рамка подтверждена | Исторический smoke и свежий CI успешны | Не распространять на MVP/target | E-004, E-015, E-016 |
| R-005 | MVP | Baseline этапа не согласован единообразно | Сопоставить выбранный состав с требованиями target и явными исключениями | Какие RFC блокируют выпуск MVP? Владелец/срок не установлены | E-004 |
| R-006 | target | Get выбранной/latest версии и список сохранённых версий: намерение установлено, wire открыт | Контрактный тест latest/explicit/unknown и metadata списка; PoC history list отсутствует | Поля, порядок, атомарность и формат списка — RFC-001 | E-005, E-006 |
| R-007 | target | Конфигурируемая N-история; не обещана вся DB-история | Put N+1 версий, проверить retention и явное чтение старых | N и переиспользование номеров/generation не финализированы | E-005, E-008 |
| R-008 | target | Delete конкретной version установлен; current semantics спорны | Delete newest/old/absent, затем latest/explicit Get; PoC newest открывает previous | MISS/current marker, Delete без version, invalidate — RFC-001 | E-005, E-006 |
| R-009 | target | Свежесть и DB/cache гонки: контракт открыт; cache CAS недостаточен | Две DB-транзакции, задержанный fill, timeout после commit, непрерывные writes | Требуемый stale bound и полнота истории; обязательства приложения | E-008, E-009 |
| R-010 | target | Не требовать отвергнутую монотонную внешнюю DB revision и строгую сериализацию всех Put | Проверить, что выбранный протокол не вводит их скрыто | Допустимые API-обязательства — RFC-001 | E-008 |
| R-011 | target | Lease/opID — предложения, не готовая гарантия exactly-once | Потеря ответа, повтор opID после смены лидера/региона; один эффект или явно unknown | Retention dedup и допустимость API — RFC-001/003 | E-009 |
| R-012 | target | Get продлевает TTL всех версий — установленное требование | До expiry прочитать ключ, проверить все версии и WAN failure | Strict либо ослабленный asyncTouch не выбран — RFC-002 | E-007 |
| R-013 | target | Eviction/admission и равенство регионов открыты | Memory pressure в A/B, eviction, повтор старой rev, restart | Authoritative eviction или независимость; thresholds unspecified | E-007 |
| R-014 | PoC | Auto-TTL исключён; replicated adapter Peek, LRU write-driven — as-built | Адресный код и сохранённый test log; нет нового TTL runtime | Не заявлять read-driven LRU или TTL-touch | E-015 |
| R-015 | target | Межрегиональная репликация нужна; async outbox — рабочее предложение | Replay/duplicate/gap/import-prefix/snapshot+tail | ACK, RYW, staleness, backlog threshold unspecified — RFC-003 | E-002, E-011 |
| R-016 | target | Региональные отказные гарантии не закрыты | WAN partition, loss A, recovery old A, overlapping promotion | RPO/RTO unspecified; кто/как fences writer — RFC-003 | E-002, E-011 |
| R-017 | target | Bulk cold warmup нужен; ручной старт допустим, механизм встроен | Потерять все копии, импортировать из источника, проверить readiness | Формат, источник, объём, DB-load и ready threshold unspecified | E-013 |
| R-018 | target | Durable recovery нужен как отдельный протокол; engine открыт | Crash/restart с log/term/vote/state/cursors/dedup, заменить peer | Retention обязательств и RTO unspecified — RFC-003 | E-013 |
| R-019 | PoC | RAM checkpoint не доказывает safe rejoin; ограничение установлено | Статический bootstrap/MemoryStorage и catch-up тест живого follower | Нельзя возвращать потерявший память старый peer ID как будто это проверено | E-013 |
| R-020 | target; PoC роли | CP конфигурирует, gateway маршрутизирует, Raft выбирает leader; роли установлены | PoC snapshot routing, heartbeat + config pull; ограниченный retry | HA/authority/stale-config и CP API — RFC-004 | E-012 |
| R-021 | target | Add/remove peer и resharding различать; migration — предложение | Learner→catch-up→committed membership→route→cleanup; отказ в каждой фазе | Number shards/hash vectors/placement не установлены; online resharding вне базы предложения | E-010 |
| R-022 | target; MVP baseline | Нужны измеримые ресурсы/нагрузка и обоснование технологий | Отчёт CPU/RAM/network/disk, CP/gateway overhead; workload с размером/скосом/версиями | p95/p99, throughput, hit rate, budgets unspecified; оценки не результаты | E-014 |
| R-023 | target; MVP baseline | Наблюдаемость обязательна; конкретный stack не принят целиком | Видимость role/term/commit/applied, lag, retries, memory, errors; проверить выбранные сигналы | Пороги alert/readiness unspecified; tracing optional в историческом MVP | E-012, E-014 |
| R-024 | PoC | Интеграция и лидерский failover реально проверены, не production acceptance | Свежий CI test/e2e/poc success; исторические smoke/race отдельно | Нет WAN/restart/performance/полной fault-matrix приёмки | E-015, E-016 |

## Приёмка без подмены scope

- Для **PoC** имеются реальные успешные outputs, но два API-расхождения R-006/R-008 и RAM-ограничение R-019 не сняты green CI.
- Для **MVP** сначала нужен явный baseline: включённые требования, исключения и критерии остановки. Его отсутствие нельзя компенсировать списком merged задач.
- Для **target** необходимы решения RFC и измерения R-022/R-023; неизвестные пороги надо согласовать, затем измерить. Авторы предложений не назначены владельцами реализации автоматически.
- Версии ранних архивных требований сохраняют значение: различия сравниваются по предмету, scope и явному замещению, не по правилу newest-wins.
