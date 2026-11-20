# OraCache storage-load

Нагрузчик для отдельного тестового кластера. Вызывает gRPC `Get` и `Put` напрямую на storage, без gateway и control plane. **Пишет в кеш: не запускай против продакшена.**

## Запуск

Нужны Go 1.27.3 и Make. Если тестовый кластер уже работает, достаточно checkout этой ветки и команды:

```sh
make run NODES=127.0.0.1:28091,127.0.0.1:28092,127.0.0.1:28093 ARGS='-read-rps=1000 -step=30s'
```

Если кластера нет, подними его из отдельного checkout основного репозитория. Ветка нагрузчика собирается без него; checkout нужен только для Compose. По умолчанию нужны свободные порты `28091–28093`.

```sh
git clone --single-branch -b storage-load https://github.com/OraCache/petruekhin-vibe.git storage-load
git clone https://github.com/OraCache/OraCache.git oracache-storage
git -C oracache-storage switch --detach 842cca19950c6aba5a9ced467ca9d1a5fd641213
cd storage-load
make cluster-up
make -s run ARGS='-read-rps=1000 -step=30s' > run.jsonl
make cluster-down
```

Путь к существующему checkout можно задать `STORAGE_DIR=...`. CLI отдельно: `make build`, затем `./bin/storage-load -help`. `make test` запускает Go-тесты с race detector, в том числе gRPC и HTTP проверку на реальных локальных сокетах, без Docker.

## Метрики

`http://127.0.0.1:9091/metrics` — отдельный HTTP-порт генератора. Метрики обновляются во время прогона, накопительно по всем измеряемым ступеням, без прогрева:

- `storage_load_slots_total{operation,state}` — запланированные слоты, отправленные RPC и пропуски генератора (`scheduler_late`, `inflight_limit`). `sent_over_5ms_late` — подмножество `sent`, а не пропуски.
- `storage_load_rpc_completed_total{operation,outcome}` — завершившиеся RPC: `ok`, ошибки gRPC и статусы кеша.
- `storage_load_success_latency_seconds{operation}` — гистограмма времени от планового старта до успешного ответа. Ошибки в неё не входят: смотри перцентили вместе с исходами и пропусками.

Пример для Prometheus на той же машине (добавить в `scrape_configs`):

```yaml
- job_name: storage-load
  scrape_interval: 2s
  scrape_timeout: 1s
  static_configs:
    - targets: ['127.0.0.1:9091']
```

Endpoint живёт только пока идёт прогон. Для коротких ступеней уменьши `scrape_interval` вместе со `scrape_timeout` или ориентируйся на итоговый JSONL: там есть результаты полностью завершившейся ступени. `-metrics-listen=IP:PORT` меняет адрес, `-metrics-listen=` отключает метрики. Если Prometheus работает на другой машине, слушай только тестовый интерфейс: у endpoint нет аутентификации.

## Профиль нагрузки

- По умолчанию: 1k / 2.5k / 5k / 7.5k / 10k Get/с по 10 с, Put в 20 раз реже. 256 прогретых ключей, значение 10 КиБ; Put без CAS. Это не тест свежести, ёмкости или TTL.
- `leader-local` читает и пишет на лидере. `rr-local` распределяет чтения по всем узлам, включая лидера; требует режима `local` на всех нодах. Для `leader-linearizable` подними кластер с `make cluster-up READ_MODE=linearizable`. Флаг `-case` не меняет настройки storage.
- Лидер определяется успешным Put при заполнении. Во время замера нет переключения лидера и прикладных повторов; `NOT_LEADER` считается отдельным исходом.
- По умолчанию слоты, опоздавшие больше чем на 5 мс, пропускаются. `-catch-up` запускает их сразу, если не упёрся в `-inflight`, и может создать всплеск. Между ступенями генератор ждёт ответы на уже отправленные RPC.
- Для сравнимых независимых прогонов пересоздавай свою тестовую группу целиком (`make cluster-down`, затем `make cluster-up`). Не перезапускай по одной ноде в живой RAM-only группе.

gRPC-контракт скопирован без изменений из того же коммита OraCache, что указан в примере. При смене wire-контракта обновляй `proto/cache/gateway.proto` и `gen/gateway/v1/` вместе.
