# Демо: регион A, холодный запуск B, замена узла в A

На показе `make` управляет контейнерами, а запросы и состав Raft меняются вручную через CLI. Go E2E запускается отдельно через `make test-e2e`.

Для показа нужны Docker Compose, Go 1.27.3 и свободные локальные порты из
[`compose.geo-demo.yaml`](../compose.geo-demo.yaml). Запускайте команды из корня
репозитория в одной сессии. Начинайте с пустого проекта `oracache-geo-demo`.

## 1. Запустить A и записать контрольный ключ

```bash
make demo-init NODES="a-1 a-2 a-3" && make demo-up-a demo-up-monitoring
go build -o bin/oracache ./cmd/oracache
export ORACACHE_ENDPOINT=127.0.0.1:38080
curl -fsS http://127.0.0.1:39466/routes
```

Дождитесь маршрута записи для партиции `0` в `region-a`. B и `a-4` пока не запущены.

```bash
bin/oracache put demo:geo before-b --expected-rev=0
bin/oracache get demo:geo
```

Проверьте `PUT_STATUS_OK` и `GET_STATUS_HIT`. Запишите `rev` из Put. A
подтверждает запись локально и держит неподтверждённое изменение для B в очереди.

[Grafana](http://127.0.0.1:33000/d/oracache-mvp) показывает трафик Gateway и роли
Raft. В [Prometheus](http://127.0.0.1:39090/targets) ещё не запущенные узлы будут
`DOWN`: это ожидаемо и не означает, что в A потерян кворум.

## 2. Запустить B и прочитать контрольный ключ напрямую

```bash
make demo-init NODES="b-1 b-2 b-3" && make demo-up-b
for port in 39191 39192 39193; do curl -fsS "http://127.0.0.1:$port/status"; done
```

Выберите узел с `Role: StateLeader` и `Ready: true`. Напрямую обратитесь к его
gRPC-порту: для `b-1`, `b-2`, `b-3` это `39091`, `39092`, `39093`
соответственно. Ниже пример для `b-1`; подставьте порт текущего лидера и `rev`
из шага 1:

```bash
ORACACHE_ENDPOINT=127.0.0.1:39091 bin/oracache get demo:geo --rev=1
```

Повторяйте только чтение, пока не получите `GET_STATUS_HIT`, ту же `rev`
и `data: "YmVmb3JlLWI="` (`before-b` в base64). Прямой линейризуемый Get исключает
перенаправление в A. Это проверка доставки очереди в B, который ещё не запускался;
она не проверяет восстановление B после потери уже подтверждённых данных.

## 3. Запустить learner и заменить voter в A

`ORACACHE_ENDPOINT` остаётся адресом Gateway из шага 1.

```bash
make demo-init NODES=a-4 && make demo-up-learner
bin/oracache membership get --group-id=region-a/shard-0/replication-1 --region=region-a
bin/oracache membership add-learner --group-id=region-a/shard-0/replication-1 --region=region-a --operation-id=add-a4 \
  --peer-id=4 --node-id=a-4 --address=a-4:28091 --raft-address=http://a-4:28191/raft
bin/oracache membership get --group-id=region-a/shard-0/replication-1 --region=region-a
curl -fsS http://127.0.0.1:38194/status
```

В составе должен появиться peer 4 с `learner: true`. Успех `add-learner`
подтверждает членство, а не завершение догоняния. Следите за `AppliedIndex`
на `a-4`; готовность к повышению проверит лидер в `replace`.

Выберите voter, который сейчас не лидер (поле `leaderId` в `membership get`).
Пример для `a-3`:

```bash
bin/oracache membership replace --group-id=region-a/shard-0/replication-1 --region=region-a --operation-id=replace-a3-with-a4 \
  --old-peer-id=3 --learner-id=4
bin/oracache membership get --group-id=region-a/shard-0/replication-1 --region=region-a
```

Если learner отстаёт или исход запроса неизвестен, повторите тот же запрос с тем
же `--operation-id`. После замены peer 4 должен быть voter, прежний peer должен
появиться в `removedIds`, а `joint` должен быть `false` (или отсутствовать в JSON).
Только после этого остановите удалённую ноду. Через тот же Gateway проверьте
исходное значение и сделайте новую запись:

```bash
make demo-stop-node ID=a-3
bin/oracache get demo:geo
bin/oracache put demo:geo after-replace
```

Прочитайте новую ревизию напрямую из лидера B, как в шаге 2. Чтобы проверить
репликацию именно с новой ноды, повторите запись после того, как `membership get`
покажет `leaderId: 4` в A.

## Проверить перевыборы и возврат ноды

В Grafana возьмите `node_id` текущего лидера A. Например, для `a-2`:

```bash
make demo-stop-node ID=a-2
```

Когда в Grafana появится новый лидер A, через Gateway прочитайте контрольный ключ и сделайте новую запись. Затем верните остановленную ноду, не меняя состав Raft:

```bash
make demo-start-node ID=a-2
```

Нода восстанавливает Raft и кеш со своего volume. Дождитесь, пока её `AppliedIndex` догонит индекс записанной операции. Если ноду уже удалили из Raft, этим способом её не возвращают. Если volume потерян, не запускайте `demo-init` для старого ID: добавьте замену с новым `PeerID` как learner.

## Остановить стенд

При ошибке посмотрите `docker compose -f compose.geo-demo.yaml logs --tail=50`.

Для повтора сбросьте весь демостенд. Команда удалит все его данные:

```bash
make demo-down
```

Порты привязаны к loopback: у клиентского и административного API нет аутентификации.
