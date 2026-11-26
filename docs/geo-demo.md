# Демо: регион A, холодный запуск B, замена узла в A

Нужны Docker Compose, Go 1.27.3, Bash и свободные локальные порты из
[`compose.geo-demo.yaml`](../compose.geo-demo.yaml). Запускайте команды из корня
репозитория в одной сессии. Начинайте с пустого проекта `oracache-geo-demo`.

## 1. Запустить A и записать контрольный ключ

```bash
dc=(docker compose -f compose.geo-demo.yaml)
"${dc[@]}" build cp-api
"${dc[@]}" up -d --no-build cp-api api-gateway a-1 a-2 a-3 prometheus grafana
go build -o bin/oracache ./cmd/oracache
export ORACACHE_ENDPOINT=127.0.0.1:38080
curl -fsS http://127.0.0.1:39466/routes
```

Дождитесь маршрута записи для партиции `0` в `region-a`. В выводе
`"${dc[@]}" --profile region-b --profile learner ps -a` не должно быть контейнеров
`b-1`, `b-2`, `b-3` или `a-4`.

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
"${dc[@]}" up -d --no-build --no-deps --no-recreate b-1 b-2 b-3
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
"${dc[@]}" up -d --no-build --no-deps --no-recreate a-4
group=(--group-id=region-a/shard-0/replication-1 --region=region-a --partition-id=0)
bin/oracache membership get "${group[@]}"
bin/oracache membership add-learner "${group[@]}" --operation-id=add-a4 \
  --peer-id=4 --node-id=a-4 --address=a-4:28091 --raft-address=http://a-4:28191/raft
bin/oracache membership get "${group[@]}"
curl -fsS http://127.0.0.1:38194/status
```

В составе должен появиться peer 4 с `learner: true`. Успех `add-learner`
подтверждает членство, а не завершение догоняния. Следите за `AppliedIndex`
на `a-4`; готовность к повышению проверит лидер в `replace`.

Выберите voter, который сейчас не лидер (поле `leaderId` в `membership get`).
Пример для `a-3`:

```bash
bin/oracache membership replace "${group[@]}" --operation-id=replace-a3-with-a4 \
  --old-peer-id=3 --learner-id=4
bin/oracache membership get "${group[@]}"
```

Если learner отстаёт или исход запроса неизвестен, повторите тот же запрос с тем
же `--operation-id`. После замены peer 4 должен быть voter, прежний peer должен
появиться в `removedIds`, а `joint` должен быть `false` (или отсутствовать в JSON).
Только после этого остановите удалённую ноду. Через тот же Gateway проверьте
исходное значение и сделайте новую запись:

```bash
"${dc[@]}" stop a-3
bin/oracache get demo:geo
bin/oracache put demo:geo after-replace
```

Прочитайте новую ревизию напрямую из лидера B, как в шаге 2. Чтобы проверить
репликацию именно с новой ноды, повторите запись после того, как `membership get`
покажет `leaderId: 4` в A.

## Остановить стенд

При ошибке сначала посмотрите `"${dc[@]}" ps -a` и `"${dc[@]}" logs --tail=50`.
Не перезапускайте отдельный остановленный узел: состояние Raft только в памяти,
а пустой процесс не должен возвращаться в работающую группу с тем же ID.
Для повтора сбросьте весь демостенд. Команда удалит все его данные:

```bash
"${dc[@]}" --profile region-b --profile learner down -v
```

Порты привязаны к loopback: у клиентского и административного API нет аутентификации.
