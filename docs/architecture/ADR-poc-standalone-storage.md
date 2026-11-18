# PoC: автономный Store + Raft

Storage-нода объединяет версионное хранилище и участника Raft в одном процессе. Все ноды получают один и тот же статический список `--peer`, но каждая слушает свои gRPC/HTTP адреса и получает собственные `--node-id` и `--peer-id`. CP и gateway для запуска не нужны.

- Записи, `Get` и `BatchGet` по умолчанию обслуживает лидер. Строгое чтение ждёт `ReadIndex` кворума и применения соответствующего индекса.
- Если адрес лидера известен, follower возвращает его в `ErrorInfo.metadata.address` (`reason=NOT_LEADER`, `domain=oracache.shardraft`, код gRPC `FailedPrecondition`). Подсказка не гарантирует, что лидер не сменился.
- `allow_stale` в `GetRequest` или в `BatchGetRequest` разрешает читать локально, в том числе с follower, который знает лидера и применил всё до своего локального commit index. Без барьера кворума результат может отставать от лидера. Для `BatchGet` режим задаётся на всю пачку полем верхнего уровня, а не флагами отдельных элементов.
- Состояние только в памяти. Остановленную ноду нельзя повторно запустить в работающем кластере под тем же ID.

Проверка с тремя нодами на реальных loopback HTTP и gRPC, без CP и gateway:

```sh
go build ./cmd/storage-node
go test -race ./internal/node -run '^TestStandaloneStorageCluster$' -count=1
```
