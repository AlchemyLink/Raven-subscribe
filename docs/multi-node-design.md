# `multi-node` — Design Doc

Status: draft, 2026-06-07.
Scope: Raven-subscribe. Затрагивает `internal/{config,database,api,syncer,xray,core}`.
Не меняет wire-формат подписки для односерверных установок (verifiable, см. §10).
Issue: [#100 multi node support](https://github.com/AlchemyLink/Raven-subscribe/issues/100).
Связанный док: [`internal-core-design.md`](internal-core-design.md) — multi-node садится поверх него.

## 1. Зачем

Сегодня один Raven управляет ровно одним Xray (локальный `config.d` + один
`xray_api_addr`). Чтобы держать несколько Xray-нод, приходится поднимать
отдельный Raven на каждой ноде со своей SQLite — дублирование стора юзеров,
рассинхрон токенов, N точек админства.

Запрос (#100): **один Raven как authoritative-стор юзеров → раздаёт юзеров на
несколько удалённых Xray-нод**, без отдельного Raven на каждой. Для небольших
приватных инсталляций это снимает главный ops-барьер.

Ключевая находка ресерча: gRPC-слой (`internal/xray/apiclient.go`) **уже
stateless и мультитаргетный** — каждая функция принимает `apiAddr` параметром
и дайлит заново. Однотаргетность сидит только в одном поле конфига
(`XrayAPIAddr`) и в вызывающем коде. А `internal/core` (Phase 1 готова) уже
вводит интерфейс `core.AdminAPI` с конструкторами `NewGRPCAdmin(apiAddr)` /
`NewFileAdmin(...)` — это ровно точка вставки мультинода.

## 2. Не-цели (явно)

- **Не вводим второе ядро.** Multi-node ≠ multi-core. Все ноды — Xray. Sing-box
  по-прежнему вне скоупа (см. `internal-core-design.md` §2). Multi-node —
  ортогонален engine-абстракции и не требует её обобщать.
- **Не делаем ноды гетерогенными по протоколу/REALITY.** Ноды **гомогенны**:
  одинаковые inbound-структуры, REALITY-ключи, SNI, VLESS Encryption.
  Различаются только `public_host:public_port` (и `api_addr`). Это «mirror
  config» — тот же принцип, что уже принят для bridge (mldsa65 compat:
  bridge mirrors EU). Гетерогенные ноды — отдельный проект, не сейчас.
- **Не вводим оркестрацию деплоя Xray.** Raven не ставит и не настраивает
  Xray на нодах — это работа Ansible. Raven лишь раздаёт юзеров на уже
  поднятые, идентично сконфигурированные ноды.
- **Не ломаем single-node путь.** При отсутствии секции `nodes` в конфиге
  поведение байт-идентично сегодняшнему (CI-инвариант, §10).
- **Не выпускаем plaintext gRPC по интернету.** См. §7 — это блокер, а не TODO.

## 3. Текущая привязка к «одной ноде» (ground truth)

Где «один Xray» зашит в код:

| Точка | Что предполагает single-node |
|---|---|
| `config.Config.XrayAPIAddr` (string) | один gRPC-адрес |
| `config.Config.APIUserInboundTag` (string) | один целевой inbound |
| `config.Config.ServerHost` + `InboundHosts`/`InboundPorts` (map by tag) | один набор endpoint-ов; host/port keyed by **tag**, не by node |
| `api/server.go` CRUD (~575-770) | `if apiAddr != "" { …ViaAPI } else { …file }` — один backend |
| `syncer/syncer.go:229` Mode-2 drift | «have»-множество читается из **локального** `config.d` через `GetExistingIdentitiesInInbound` (файлы на диске) |
| `syncer/status.go` `SyncStatus` | один глобальный health-снимок |
| `xray/apiclient.go` | **уже мультитаргетный** — единственное место, что НЕ надо менять |

Логически single-node допущение живёт в 4 местах: config, БД-модель, CRUD-роутинг
в api, и drift/status в syncer. Цель доки — расширить ровно эти 4, оставив
gRPC-слой как есть и не тронув локальный путь.

## 4. Модель ноды

```jsonc
// config.json — новая опциональная секция. Отсутствует → single-node, как сегодня.
{
  "server_host": "eu.example.com",        // legacy: остаётся как implicit local node
  "xray_api_addr": "127.0.0.1:10085",     // legacy
  "api_user_inbound_tag": "vless-reality-in",

  "nodes": [
    {
      "name": "eu-1",                      // стабильный id ноды (FK в БД)
      "api_addr": "10.7.0.1:10085",        // gRPC HandlerService, ТОЛЬКО на WG-адресе (§7)
      "inbound_tag": "vless-reality-in",   // в какой inbound лить юзеров
      "public_host": "eu.example.com",     // что попадёт в outbound клиентского конфига
      "public_port": 443,
      "enabled": true,                     // нода в ротации генератора/синка
      "deploy": {                          // ОПЦИОНАЛЬНО, только для file-backend нод
        "mode": "grpc"                     // "grpc" (default) | "ssh_rsync"
      }
    },
    { "name": "eu-2", "api_addr": "10.7.0.2:10085",
      "inbound_tag": "vless-reality-in", "public_host": "eu2.example.com",
      "public_port": 443, "enabled": true },

    { "name": "eu-3",                      // нода БЕЗ WG → gRPC по публичному адресу под mTLS
      "api_addr": "203.0.113.7:10085",     // публичный адрес разрешён, т.к. есть tls-блок (§7)
      "inbound_tag": "vless-reality-in", "public_host": "eu3.example.com",
      "public_port": 443, "enabled": true,
      "tls": {                             // ОПЦИОНАЛЬНО, mTLS на gRPC-дайал к этой ноде
        "ca_cert":     "/etc/raven/pki/node-ca.pem",   // CA, подписавший server-cert ноды
        "client_cert": "/etc/raven/pki/raven-client.pem",
        "client_key":  "/etc/raven/pki/raven-client.key",
        "server_name": "eu-3.internal"     // пусто → host из api_addr
      }
    }
  ]
}
```

Правила обратной совместимости (в `config.Load`):
- `nodes` отсутствует/пуст → синтезируем **одну** implicit-ноду `name="local"`
  из legacy-полей (`xray_api_addr`/`api_user_inbound_tag`/`server_host`). Весь
  downstream-код работает только с `[]Node`, single-node — частный случай N=1.
- `nodes` задан → legacy-поля игнорируются для провижининга (но `server_host`
  остаётся дефолтом для `HostForInbound`/балансера, если у ноды пусто).
- Валидация: `name` уникален и непустой; `api_addr` непуст; при `mode="grpc"`
  адрес обязан быть в private-диапазоне ИЛИ иметь `tls`-блок (mTLS) ИЛИ явный
  флаг `allow_public_grpc:true` (анти-footgun против plaintext-по-интернету, §7).
  `tls`-блок, если задан, требует `ca_cert`+`client_cert`+`client_key`.

## 5. Модель БД

Сейчас: `users`, `inbounds`, `user_clients(user_id, inbound_id, config_json)`,
`app_settings`, `emergency_profiles`. Концепта ноды нет.

Решение **A2 — per-node opt-in** (как просит автор: «enabled on selected
targets»). Goose-миграция, additive, дефолт = no-op для односерверных:

```sql
-- migration NNN_multi_node.sql  (+goose Up)
CREATE TABLE IF NOT EXISTS nodes (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL UNIQUE,
    api_addr    TEXT NOT NULL,
    inbound_tag TEXT NOT NULL,
    public_host TEXT NOT NULL,
    public_port INTEGER NOT NULL,
    enabled     INTEGER NOT NULL DEFAULT 1,
    created_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- какие юзеры на какой ноде. Отсутствие строки = юзер НЕ на ноде.
CREATE TABLE IF NOT EXISTS user_nodes (
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    node_id INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, node_id)
);
CREATE INDEX IF NOT EXISTS idx_user_nodes_node ON user_nodes(node_id);
```

Бэкфилл при апгрейде (в `+goose Up`, идемпотентно):
1. Если в конфиге одна implicit-нода → создать строку `nodes('local', …)` и
   `INSERT INTO user_nodes SELECT id, <local_node_id> FROM users` (все
   существующие юзеры → на локальной ноде). Поведение не меняется.
2. Конфиг — источник истины для строк `nodes`: на старте Raven делает
   reconcile `config.nodes` → таблицу `nodes` (upsert по `name`, пометка
   `enabled=0` для исчезнувших, без удаления — чтобы `user_nodes` FK выжили).

Invariant: `user_clients` (credential-блоб) остаётся **per-inbound**, не
per-node. Поскольку ноды гомогенны (§2), один и тот же credential валиден на
всех нодах с этим `inbound_tag`. `user_nodes` управляет лишь *размещением*,
не credential-ами. Это держит миграцию маленькой.

## 6. Изменения по слоям

### 6.1 `internal/core` — fan-out как `AdminAPI`

Никаких новых интерфейсов. Multi-node — это **композитная реализация**
существующего `core.AdminAPI`:

```go
// internal/xray/fanout.go
type fanoutAdmin struct {
    targets []namedAdmin // {name string; admin core.AdminAPI (grpc per node)}
}

func NewFanoutAdmin(nodes []core.NodeTarget) core.AdminAPI { … }

// AddClient на fan-out = AddClient на каждой целевой ноде; собирает per-node
// результат. Частичный успех НЕ откатывается (см. §6.4 — это by design:
// «partial failures remain visible»), а репортится наверх.
func (f *fanoutAdmin) AddClient(inboundTag, identity string, h core.AddClientHint) (string, error)
```

Важно: `AddClient` должен вернуть **один** `storedConfigJSON` для записи в БД.
Поскольку ноды гомогенны, credential идентичен на всех — берём с первой
успешной ноды, остальные сверяем (mismatch = config-drift между нодами,
логируем громко). Это закрывает open-question «какой config_json писать».

Сигнатуры `core.AdminAPI` (`AddClient/RemoveClient/AddInboundFromJSON/…`)
**не меняются** — fan-out прячется за тем же интерфейсом. `api.Server`,
получивший поле `admin core.AdminAPI`, не замечает разницы между одной нодой и N.

**Состояние core-рефактора (verified 2026-06-07):** заморожен на Phase 1 —
все 4 интерфейса и value-типы объявлены (`internal/core/*.go`,
`TestCoreInvariants`), но `internal/xray` их **не имплементирует** (нет
`impl.go`, нет `NewGRPCAdmin/NewFileAdmin`), а `api`/`syncer` импортируют
`xray` напрямую — **28 call-site**. Поэтому multi-node не ждёт весь рефактор
(§9): берём **узкую адопцию** — имплементируем только `core.AdminAPI`
(write-path) + инъекция в `Server`. Полную миграцию builder/parser/syncer
(Phase 3/4 internal-core-design) оставляем отдельным проектом.

### 6.2 `internal/api` — CRUD и выбор нод

- `POST /api/users` принимает опциональный `nodes: ["eu-1","eu-2"]`. Пусто →
  все `enabled` ноды (или дефолт-политика). Заполняет `user_nodes`.
- CRUD идёт через `s.admin` (= fanoutAdmin) — никаких `if apiAddr` ветвей
  (их и так убирает Фаза 3 core-рефактора).
- Новые admin-эндпоинты: `GET/POST/DELETE /api/nodes`, `POST /api/users/{id}/nodes`.

### 6.3 `internal/xray/generator.go` — outbound на ноду

Сегодня генератор делает outbound(s) из inbound-структуры + `InboundHosts`/
`InboundPorts` (keyed by tag). Для мультинода:

- для каждой ноды, где юзер размещён (`user_nodes`), эмитим **один outbound**
  с `node.public_host:node.public_port`, наследуя protocol/security/REALITY от
  `inbound_tag`-структуры;
- все эти outbound-ы кладём под **существующий балансер** (`BalancerStrategy`).
  Клиент сам выбирает живую ноду по leastPing/leastLoad — бесплатно из коробки.

Это и есть «subscription generation includes target endpoint selection» из
issue: endpoint-ы = ноды юзера, селекция = балансер.

Wire-инвариант: при N=1 (single-node) outbound байт-идентичен сегодняшнему
(golden-тест, §10).

### 6.4 `internal/syncer` — per-node drift/status

**Verified 2026-06-07 против `xray-core v1.260327.0`:** HandlerService **отдаёт
список юзеров инбаунда через gRPC** — `GetInboundUsers(tag, email="")` →
`um.GetUsers(ctx)` (весь список), плюс `GetInboundUsersCount(tag)` и
`ListInbounds(isOnlyTags)`
(`app/proxyman/command/command.go:125-163`, `command.proto:48-97`). Эти RPC
**не обёрнуты** в нашем `internal/xray/apiclient.go` сегодня — но доступны.

Следствие: для удалённой ноды доступен **полноценный reconcile по сети** —
«have»-множество берём `GetInboundUsers`, «want» — из БД, diff устраняем
`AddUser`/`RemoveUser`. Это та же семантика, что у локального config.d-пути,
но без зависимости от файлов на диске.

Модель для удалённых gRPC-нод — **реальный diff (DB=истина)**:
- периодически (по `SyncInterval`) для каждой `enabled`-ноды:
  `have = GetInboundUsers(tag)`; `want = user_nodes ∩ inbound`;
  добавить `want\have` через `AddUser`, опционально удалить `have\want`;
- `AddUser` на дубль → `"User X already exists."`; `RemoveUser` отсутствующего
  → `"User X not found."` — fan-out **обязан** глотать обе как benign
  (string-match), иначе idempotent re-apply спамит ошибками. Сейчас apiclient
  глотает exist-ошибку только для AddInbound (см. §13);
- per-node health = (gRPC reachable) + (drift count после diff).

**⚠️ Рестарт ноды стирает gRPC-юзеров (см. §13).** Reconcile-тик —
не только drift-страховка, а **обязательный** recovery-механизм: после
рестарта удалённой ноды все её юзера исчезают до следующего прохода.
Для gRPC-only нод это означает outage ≤ `SyncInterval`.

**Бонус-упрощение (вне скоупа multi-node, но открывается):** локальный путь
сейчас в gRPC-режиме читает диск через `GetExistingIdentitiesInInbound`
(`syncer.go:237`). После обёртки `GetInboundUsers` обе ветви (local/remote)
могут унифицироваться на gRPC, убрав disk-read из drift-детекта целиком.
Не обязательно для multi-node — отметить как follow-up.

Локальная нода (с `config_dir`) до унификации продолжает использовать
существующий config.d-based drift из `syncXray()` **без изменений**.

`SyncStatus` → per-node:

```go
type MultiSyncStatus struct {
    Nodes map[string]NodeSyncStatus `json:"nodes"` // ключ = node.name
}
type NodeSyncStatus struct {
    SyncStatus               // переиспользуем существующую структуру
    Reachable   bool   `json:"reachable"`     // последний gRPC-дайл удался
    LastApplyOK bool   `json:"last_apply_ok"`
    UsersTarget int    `json:"users_target"`  // сколько должно быть (DB)
    ApplyErrors int    `json:"apply_errors"`
}
```

`/api/sync/status` отдаёт `MultiSyncStatus`. Для single-node — один ключ
`"local"`, форма расширяется аддитивно (dashboard читает `.nodes["local"]`).
Это и есть «sync status displays per-target rather than global» + «partial
failures remain visible».

## 7. Security (блокер, не TODO)

HandlerService = полный контроль над юзерами/инбаундами (и, без per-RPC ACL,
над inbound/outbound — §13). По интернету без auth/TLS это критическая дыра:
кто угодно в сети добавит/удалит юзеров на всех нодах. **Multi-node нельзя
выпускать с plaintext gRPC по публичной сети.** Транспорт выбирается
**per-node по `api_addr`** в единственном chokepoint `dialXrayAPI`
(`resolveCredentials`): нода из mTLS-мапы → TLS, любой другой адрес
(WG/loopback) → plaintext. Два пути, в порядке предпочтения:

1. **gRPC только по WireGuard (дефолт-рекомендация).** Xray API слушает на
   WG-адресе ноды; шифрование и взаимная аутентификация — на уровне WG. gRPC
   остаётся plaintext, но недостижим извне туннеля. Ложится на нашу
   существующую EU/RU mesh (`f3_second_ru_research`), не требует gRPC-TLS-обвязки
   и ротации сертов. Конфиг: `api_addr` в private-диапазоне, без `tls`-блока —
   валидация проходит по `isPrivateHostPort`.
2. **mTLS на gRPC (для нод без WG).** Реализовано (Фаза 5): опциональный
   per-node `tls`-блок `{ca_cert, client_cert, client_key, server_name?}`.
   Raven — TLS-клиент: предъявляет client-cert, проверяет server-cert по CA;
   `server_name` пусто → host из `api_addr`; форсируется TLS 1.3. Серты читаются
   **один раз на старте** (`main.configureNodeCredentials` → `xray.BuildTLSCredentials`
   + `SetNodeCredentials`), **fail-closed** — битый cert = fatal, никогда не
   молчаливый откат на plaintext против публичного адреса. Cert-provisioning
   (server-cert + clientAuth на Xray-side) делает Ansible-роль ноды.

Анти-footgun в `config.Load` (§4): `mode="grpc"` с публичным `api_addr` и без
защиты → ошибка старта. Guard снимается **любым** из: private/WG-адрес,
`tls`-блок (mTLS сам по себе — шифрование+аутентификация), или явный
`allow_public_grpc:true` (escape-hatch для внешней mTLS-терминации sidecar'ом).

## 8. SSH/rsync деплой — механизм durability, не просто опция

Изначально считал rsync second-class опцией. Ресерч рантайма Xray (§13)
переквалифицирует его: **gRPC-мутации эфемерны (рантайм-онли), rsync в
`config.d` даёт durability через рестарт ноды.** Это две ортогональные оси:

| | live-апдейт | переживает рестарт ноды |
|---|---|---|
| gRPC-only | ✅ мгновенно | ❌ юзера теряются до reconcile |
| rsync config.d only | ❌ только при следующем pull/reload | ✅ |
| **gRPC + rsync** (как локальная нода) | ✅ | ✅ |

Локальная нода уже делает оба (dual-write gRPC + `config.d`, `RestoreOnStartup`).
Удалённая нода для той же durability требует, чтобы Raven писал в её `config.d`
— а это и есть SSH/rsync.

Решение по приоритету:
- **Итерация 1 — gRPC-only**, durability через частый reconcile (outage ≤
  `SyncInterval` после рестарта ноды). Достаточно для малых инсталляций.
  `deploy.mode="ssh_rsync"` заложен в схему, но возвращает «not implemented».
- **Итерация 2 (по спросу) — gRPC + rsync** для durability. Минусы остаются:
  SSH-ключи, sudo, race с reload; по умолчанию выключен.

## 9. План миграции

Фазы мерджатся отдельными PR; на каждом `make build && go test ./... -race`
и integration-тесты зелёные; прод не трогается до Фазы 5.

### Фаза 0 — узкая адопция `core.AdminAPI` (НЕ весь рефактор)
Предусловие, но **только write-path**, не вся internal-core миграция:
- `internal/xray/impl.go`: `NewGRPCAdmin(apiAddr)` и `NewFileAdmin(dir, perm)`
  — тонкие методы-обёртки над существующими свободными функциями
  (`AddClientToInboundViaAPI`/`AddExistingClientToInboundViaAPI`/
  `RemoveUserFromInboundViaAPI`/`AddInboundFromJSONViaAPI`/`RemoveInboundViaAPI`
  и file-аналоги). Static-check `var _ core.AdminAPI = (*grpcAdmin)(nil)`.
- `api.Server` получает поле `admin core.AdminAPI`; **5 разбросанных
  if/else-веток** (`addUserToInbound` server.go:612-616, `removeUserFromXray`
  :682-697, `addClientToXray` :710-720, `removeClientFromXray` :729-743,
  `addUserToXray` :756-771) схлопываются в `s.admin.AddClient(...)` и т.д.
- `parser`/`builder`/`sub_links` миграцию (Phase 3/4 internal-core-design)
  **НЕ трогаем** — они multi-node не нужны.

**Риск:** низкий-средний (write-path локализован в ~5 методах одного файла).
**Acceptance:** существующие unit+integration зелёные; поведение N=1 идентично;
`grep -n 'XrayAPIAddr' internal/api/server.go` → только в сборке `admin`, не в
CRUD-ветвях.

### Фаза 1 — config + БД, additive, single-node no-op
`config.nodes` секция + синтез implicit-ноды; goose-миграция `nodes`/
`user_nodes` + бэкфилл. Downstream ещё не использует — только модель.
**Риск:** низкий. **Acceptance:** апгрейд существующей БД → все юзеры на
`local`, подписки байт-идентичны.

### Фаза 2 — `fanoutAdmin`
`internal/xray/fanout.go` реализует `core.AdminAPI` поверх `[]NewGRPCAdmin`.
`main.go` собирает fanout из `nodes`. CRUD начинает раскидывать веером.
**Риск:** низкий-средний. **Acceptance:** unit на partial-failure
(2 ноды, одна падает → юзер на живой, статус показывает провал второй).

### Фаза 3 — генератор per-node outbounds + балансер
`user_nodes` → один outbound на ноду под балансером.
**Риск:** средний (wire-формат). **Acceptance:** golden-тест N=1
байт-в-байт со старым; новый golden для N=2.

### Фаза 4 — per-node status + dashboard
`MultiSyncStatus`, `/api/sync/status` расширен; idempotent re-apply loop для
remote-нод; admin-эндпоинты `/api/nodes`.
**Риск:** низкий. **Acceptance:** down-нода видна как `reachable:false`,
не роняет sync остальных.

### Фаза 5 — security + docs + Ansible
WG-bound gRPC как дефолт (рекомендация); `allow_public_grpc` guard; опциональный
mTLS. **mTLS — ГОТОВО** (per-node `tls`-блок, §7): `dialXrayAPI` резолвит creds
per-`api_addr`, WG/loopback остаётся plaintext, mTLS снимает public-grpc guard,
fail-closed на старте, TLS 1.3. Остаётся: README + INSTALL multi-node секция;
Ansible-роль для homogeneous-нод (cross-check с Raven-server-install) —
provision server-cert + `clientAuth` на Xray gRPC-инбаунде. **Риск:** низкий.

## 10. Приёмка (инвариант «не сломали subscriber»)

- При **отсутствии** `nodes` в конфиге:
  - все unit + integration тесты зелёные;
  - `/sub/{token}` (все view: full / links_txt / links_b64 / compact) и
    `/c/{token}` дают **байт-идентичный** ответ до и после, на golden-наборе
    (VLESS+REALITY, VLESS+XHTTP, fallback);
  - `/api/sync/status` содержит ровно один ключ `"local"` с теми же полями.
- При `nodes=[A,B]` гомогенных:
  - юзер, размещённый на A и B, получает балансер из 2 outbound-ов;
  - падение B → юзер всё ещё рабочий через A; `status.nodes["eu-2"].reachable=false`;
  - plaintext gRPC на публичный адрес без `tls`/`allow_public_grpc` → отказ старта;
  - нода с `tls`-блоком и публичным `api_addr` → старт разрешён (mTLS = guard);
  - `tls`-блок с битым/отсутствующим cert → fatal на старте (fail-closed).
- `insecure.NewCredentials()` живёт только в `resolveCredentials` (дефолт для
  WG/loopback-нод); все mTLS-ноды дайлятся через `credentials.NewTLS` из
  `SetNodeCredentials`-мапы (security-ревью Фазы 5).

## 11. Открытые вопросы

- **Дефолт-политика размещения новых юзеров.** Все `enabled`-ноды, или явный
  выбор обязателен? Предложение: дефолт = все `enabled`, переопределяемо
  `nodes:[...]` в `POST /api/users`. Решить в Фазе 2.
- **Reality-ключи per-node или общие.** §2 фиксирует гомогенность (общие).
  Если кому-то нужны разные ключи — это гетерогенные ноды, отдельный проект.
  Зафиксировать в README как ограничение.
- **Emergency rotation × multi-node.** `fallback.go` поднимает inbound через
  gRPC на одном адресе. При мультиноде killswitch/rotation должны идти веером
  тоже. Вне скоупа первой итерации — но `fanoutAdmin` покрывает
  `AddInboundFromJSON`/`RemoveInbound` тем же fan-out, так что почти бесплатно.
  Отметить в Фазе 4.
- **Stats/метрики per-node.** xray-stats-exporter сейчас скрейпит один Xray.
  Multi-node → N экспортеров или один мульти-таргетный. Вне скоупа этого дока
  (это Raven-subscribe), но отметить для observability-роадмапа.

## 12. Что это **не** даёт

- Не делает ноды гетерогенными (разные протоколы/ключи) — §2.
- Не оркестрирует деплой Xray — это Ansible.
- Не решает SPOF самого Raven (один control-plane = одна SQLite). HA Raven —
  другой проект (вероятно, replicated store, чего мы сознательно избегаем,
  см. internal-core-design §10).
- Не повышает throughput одной ноды — это про горизонтальное размещение
  юзеров, не про производительность.

## 13. Жизнеспособность Xray в многонодовом режиме (verified 2026-06-07)

Изучение `xray-core v1.260327.0` (vendored) на предмет рантайм-семантики,
которая решает, работает ли схема «один control-plane → N Xray по gRPC».

### 13.1 gRPC-мутации эфемерны (центральный факт)

Весь user-state — `MemoryValidator` (`sync.Map`, `proxy/vless/validator.go:28`;
аналогично trojan/vmess/shadowsocks). `AddInboundHandler`
(`core/xray.go:98`) — рантайм-регистрация. Во всём `app/proxyman` и
`app/commander` **ноль** дисковой персистентности (`WriteFile`/`SaveConfig`).

**Следствие:** рестарт Xray на ноде стирает всех, кто был добавлен только по
gRPC. Переживают рестарт лишь юзера в `config.d` (грузятся при старте).
- Локальная нода: уже закрыто dual-write (gRPC + `config.d`) +
  `RestoreOnStartup` (`server.go:688`, `syncer.go:105`).
- Удалённая gRPC-only нода: локального `config.d` нет → её рестарт = полный
  отказ юзеров до следующего reconcile. **Reconcile-тик обязателен как
  recovery, не только как drift-страховка.** Durability — через rsync (§8).

### 13.2 Семантика ошибок (idempotency)

| Операция | На дубль / отсутствие | Источник |
|---|---|---|
| `AddUser` существующего | `"User X already exists."` | `vless/validator.go:39`, `trojan/validator.go:23` |
| `RemoveUser` отсутствующего | `"User X not found."` | `vless/validator.go:54`, `shadowsocks/validator.go:66` |
| `AddInbound` существующего | `*exist*` (уже глотаем) | `fallback.go` |

Fan-out reconcile **обязан** string-match'ить «already exists» (AddUser) и
«not found» (RemoveUser) как benign. Текущий `apiclient.go` глотает только
exist для AddInbound — для AddUser/RemoveUser нужно добавить.

### 13.3 `GetInboundUsers` отдаёт рантайм-состояние

`GetUsers` → `validator.GetAll()` (`vless/inbound/inbound.go:256`) ранжирует по
map `email`. Diff корректен (видит и gRPC-, и config-загруженных). Edge-case:
юзер с **пустым email не вернётся** (`GetAll` идёт по `v.email`). У нас email
всегда задан (username) — ок, но для чужих конфигов отметить.

### 13.4 Ноды = единый trust-домен (REALITY)

Гомогенность требует **идентичных** REALITY `privateKey`, `shortIds` (и
`mldsa65Seed`, если PQ) на всех нодах — иначе один клиентский конфиг не
аутентифицируется через балансер. Цена: компрометация ключа на любой ноде =
компрометация REALITY-идентичности всех нод. Это та же модель, что у нашего
bridge (mirror EU, `feedback_mldsa65_compat`) — приемлемо, но задокументировать
в README как явное свойство безопасности.

### 13.5 Предусловие на ноде (Ansible, не Raven)

Каждая нода должна иметь:
- `api`-inbound + routing-rule `inboundTag:["api"]` + `services` со списком
  `HandlerService` (юзеры) и опц. `StatsService` (метрики);
- API-listener на **WG-адресе** (§7), не на `0.0.0.0`;
- идентичную inbound-структуру (tag, протокол, REALITY) — homogeneous.

Raven Xray не настраивает — ноды провижинятся Ansible-ролью заранее (Фаза 5).

### 13.6 Stats размазаны по нодам

`StatsService` — per-instance. При балансировке трафик юзера дробится по нодам,
counters живут на той, что обслужила сессию. Агрегация per-user = сумма по
нодам. Касается `xray-stats-exporter` (N target'ов или мульти-скрейп), вне
scope этого дока, но критично для квот/биллинга — отметить в observability.

### 13.7 Вердикт

Схема **жизнеспособна** при соблюдении: (1) reconcile как recovery после
рестарта ноды, (2) benign-обработка idempotency-ошибок, (3) гомогенные ноды
с общим REALITY-ключом в одном trust-домене, (4) gRPC только по WG, (5)
ноды предварительно провижинятся Ansible с `api`-inbound. Durability через
рестарт для gRPC-only нод ограничена `SyncInterval` — для строгой durability
нужен rsync-путь (§8, итерация 2).

## 14. Индустриальные паттерны (enterprise Xray multi-node, ресерч 2026-06-07)

Как multi-node Xray реально устроен у зрелых OSS-панелей — внешняя валидация
нашего дизайна.

### 14.1 Консенсус: node-агент, НЕ прямой cross-network Xray-gRPC

| Панель | Node-компонент | Panel↔node | Engine на ноде |
|---|---|---|---|
| **Marzban** | `marzban-node` (агент) | REST/HTTPS или RPyC, SSL-серты | Xray (агент владеет lifecycle) |
| **Marzneshin** (форк Marzban «ради scalability») | `marznode` (агент) | **gRPC + client SSL cert** (`CLIENT_SSL_CERT`) | Xray / Hysteria / sing-box (multi-backend) |
| **Remnawave** | `remnanode` (агент, NestJS+Xray) | push конфига по «secure internal API», `NODE_PORT` фаерволлится только на IP панели | Xray |
| **Hiddify-Manager** | — (per-server install) | multi-node — **открытый запрос** (issue #5111) | Xray/sing-box |

**Все, у кого multi-node зрелый, ставят агент на ноду.** Никто не дёргает
Xray HandlerService напрямую по сети из центра. Агент даёт ровно то, чего нет
у нашего raw-gRPC: **durability** (агент держит конфиг на ноде → сам
поднимает Xray после рестарта, не завися от reconnect панели), **автономию**
(Remnawave: нода работает, даже если панель оффлайн), **чистую security-границу**
(свой протокол + TLS + firewall-на-IP, а не голый Xray API), **config push**
(агент провижинит inbound-ы, а не только юзеров) и **сбор stats**.

### 14.2 Чему это учит наш дизайн

- **Наш agentless-выбор — осознанное отклонение от мейнстрима, не незнание.**
  Оправдан масштабом (мы не панель на тысячи нод), наличием WG-mesh и Ansible,
  и явным запросом автора issue «no separate Raven on each node». Но **durability-
  gap реален именно потому, что из-за него все остальные и держат агент** —
  значит reconcile+rsync (§8, §13.1) это не опция, а обязательная компенсация.
- **Наш live-delta через `AlterInbound` ЛУЧШE, чем у Marzban.** Marzban
  исторически переписывает `config.json` и **рестартит Xray** на изменение/
  истечение юзера → рвёт чужие коннекты (3x-ui issue #4777, Marzban #105). Мы
  меняем юзеров горячим gRPC без рестарта — это преимущество, сохранить.
- **Config push мы сознательно НЕ делаем** (inbound-ы провижинит Ansible) —
  агентные панели делают. Для нас ок: ноды гомогенны и статичны.
- **Multi-backend (xray/hysteria/singbox) выбрал именно scalability-форк
  (marznode).** Мы в `internal-core-design §2` это явно отвергли. Trade-off
  осознан: мы оптимизируем под Xray-only, не под «любой engine».
- **Stats per-node — нерешённая проблема у всех.** Marzban вводит
  `usage_coefficient` (множитель биллинга на ноду) + центральный сбор. Если
  пойдём в квоты/биллинг — нужен аналогичный мульти-таргет сбор
  (`xray-stats-exporter`, §13.6).

### 14.3 Growth-path

Если AlchemyLink когда-нибудь вырастет в продукт со множеством нод/продажей —
**agent-модель (`raven-node`) становится оправданной**: durability + автономия +
security-граница перевесят стоимость лишнего компонента. Тогда узкая
`core.AdminAPI`-адопция (Фаза 0) переиспользуется: `raven-node` имплементирует
тот же интерфейс локально, а fan-out из центра ходит в агенты вместо голого
Xray. То есть **текущий agentless-дизайн — не тупик, а первый шаг**, совместимый
с будущим агентом. Перформанс-заметка (для sizing ноды, не control-plane):
Xray держит ~40-55 KB/conn, память растёт до ~1 GB и не освобождается; при
тысячах юзеров нужен OS-тюнинг (`fs.file-max`, `somaxconn`, `nofile`) — нода,
а не Raven, упирается первой.

## 15. Агентная модель и единая точка управления конфигами — углублённый разбор

Запрос: рассмотреть **агентный** вариант и **единую точку управления
конфигами** всерьёз. Все четыре драйвера признаны интересными: durability,
автономия при оффлайн-панели, устранение Ansible↔Raven split-brain,
динамическое управление конфигом нод. Этот раздел разбирает их без упрощений
и приходит к нетривиальному выводу: **правильно урезанный тонкий агент
безопаснее нашего же agentless-варианта** — то есть аргумент ЗА агент есть
даже на нашем масштабе, но не тот, что у Marzban.

### 15.1 Две независимые оси (их нельзя смешивать)

«Агент» и «единая точка управления конфигами» — **разные** решения:

- **Ось 1 — runtime-plane**: кто применяет user-/lifecycle-операции к Xray.
  Варианты: голый Xray-gRPC из центра (A) ↔ агент на ноде (B/C).
- **Ось 2 — config-plane**: кто источник истины для inbound/REALITY/routing.
  Варианты: git+Ansible (A/B) ↔ Raven как authority (C).

Драйверы ложатся на разные оси: durability/автономия — **ось 1**;
split-brain/динам.конфиг — **ось 2**. Поэтому «давайте сделаем агентов» и
«давайте единую точку конфигов» — это два решения, и их надо принимать
отдельно.

### 15.2 Seizure-переоценка: голый gRPC скрытно ОПАСНЕЕ агента

Раньше (§7, §14) я отнёс agentless-A к «низкому seizure-радиусу», потому что
Raven не *хранит* конфиги. Это **неполно**. Голый Xray HandlerService
**не имеет per-RPC ACL** — кто дотянулся до порта, тот может всё:
`AddInbound` (поднять backdoor-листенер), `AddOutbound` (перенаправить
трафик), `RemoveInbound` (DoS), не только `AddUser`
(`app/proxyman/command/command.go` — все RPC на одном сервисе без авторизации).

В модели A Raven по WG достаёт HandlerService **каждой** ноды. Значит
**захват Raven = полный контроль рантайм-конфига всех нод**, даже без хранения
ключей: атакующий добавляет свои inbound/outbound на весь флот. «Не хранит
конфиг» ≠ «не может его менять».

| Модель | Raven *хранит* ключи/конфиг | Raven *может* на ноде | Seizure-радиус |
|---|---|---|---|
| **A** agentless raw-gRPC | нет | **всё** (Add/RemoveInbound/Outbound) — Xray API без ACL | средне-высокий (полный runtime-контроль флота) |
| **B** тонкий агент, capability-урезанный | нет | **только** user-ops (агент не отдаёт inbound/outbound-API) | **низкий** (макс. add/remove юзеров — ротируемо) |
| **C** полный control-plane | **да** (все ключи) | всё + хранит истину | высокий |

**Вывод, разворачивающий §14:** тонкий агент, который наружу отдаёт Raven'у
**только** `AddUser/RemoveUser/GetUsers/GetStats` (а `AddInbound` и т.п. —
нет), даёт **меньший** seizure-радиус, чем наш agentless raw-gRPC. Это
capability-confinement, которого Xray сам по себе не умеет. Так что «смысл в
агенте» есть и под нашу threat-model — но ценность не durability (её даёт и
rsync), а **сужение того, что захваченный центр может сделать с нодами**.

### 15.3 Как сделать config-plane без амплификации захвата

Если хотим и единую точку конфигов (ось 2), но без «захватил Raven → пушит
малварь на все ноды» — ключ в **разделении capability** и **подписи**:

1. **Pull, а не push.** Агент сам тянет конфиг-артефакт (из git/CI/artifact
   store), Raven его не пушит. Захват Raven не даёт push-канала.
2. **Подписанные конфиг-бандлы, ключ подписи — оффлайн.** Конфиг подписывается
   ключом оператора (laptop/CI/HSM), агент отвергает неподписанное/чужой
   подписи. Захваченный Raven может раздать только уже подписанные (старые)
   бандлы — не сфабриковать новый малварный.
3. **Capability split = главный принцип.** Always-on плоскость (Raven, юзеры)
   держит только low-blast-radius операции; high-value (ключи/inbound/routing)
   живёт в git+vault и приезжает подписанным бандлом. Разделяем «что онлайн и
   достижимо» от «что ценно».
4. **Per-node scoping.** mTLS client-cert на ноду; агент применяет команды
   только для своего node-id. Узкий креденшл Raven→агент (user-ops scope).

Это превращает «единую точку конфигов» из **push-authority** (опасно) в
**signed-distribution** (безопасно): единый логический источник = git, агент
верифицирует подпись, Raven в config-цепочке — максимум транспорт, не доверенный.

### 15.4 Что конкретно «переезжает» в каждой модели

| Объект | A (текущий) | B (тонкий агент) | B+ (агент + signed config) | C (full plane) |
|---|---|---|---|---|
| Юзеры (CRUD) | Raven→gRPC | Raven→агент | Raven→агент | Raven |
| Durability юзеров при рестарте | reconcile/rsync | агент (локальный store) | агент | агент |
| inbound-структура | Ansible | Ansible | git→signed→агент pull | Raven push |
| REALITY-ключи/секреты | Ansible+vault | Ansible+vault | git+vault→signed | **Raven БД** |
| routing/outbounds | Ansible | Ansible | git→signed | Raven push |
| emergency rotation | Raven gRPC (есть) | Raven→агент | Raven→агент | Raven |
| stats | exporter скрейпит Xray | агент relays | агент relays | агент relays |
| split-brain убран? | нет (есть contract-test опция) | нет | **да** (один signed источник) | да |
| seizure-радиус | средне-высокий | низкий | низкий | высокий |

### 15.5 Драйверы → что их закрывает

| Драйвер | Минимально достаточно | Заметка |
|---|---|---|
| Durability при рестарте | A+rsync **или** B | агент не обязателен |
| Автономия при оффлайн-панели | **B** (только агент) | rsync не даёт автономию управления, только данные |
| Убить split-brain | contract-тесты (дёшево) **или** B+/C | git-источник + verify |
| Динам. управление конфигом нод | B+ (signed pull) **или** C | C опаснее по seizure |

Все четыре сразу закрывает **B+** (тонкий агент + pull-based signed config),
**не** трогая Raven как authority ключей — то есть без seizure-амплификации C.

### 15.6 Рекомендация

**Цель-максимум: B+ — capability-урезанный `raven-node` + pull-based signed
config из git.** Закрывает все четыре драйвера И *улучшает* seizure-постуру
против текущего agentless-A. Config authority остаётся git/vault (не дублируем
Ansible как источник, но Ansible сводится к bootstrap агента; рендер
конфига можно оставить в Ansible-CI как «подписывающий» шаг).

**Чего НЕ делать: наивный C** (Raven хранит ключи + push-authority) — единая
точка ценой максимального seizure-радиуса и дублирования vault. Под нашу
threat-model (A2 seizure-reduction, RU-compromise runbook) — плохой размен.

**Прагматичный interim (если B+ не сейчас): A + rsync** (§8) — durability без
агента, но **без** capability-confinement и без автономии. Сознательно
принимаем, что захваченный Raven имеет полный runtime-контроль флота (риск
такой же, как сегодня у single-node EU, просто умноженный на число нод).

**Порядок, совместимый с уже описанными фазами:**
- Фаза 0-4 (§9) реализуют **A** — это база и first step (узкая `core.AdminAPI`).
- **B+ как Фаза 6** (новая, по решению): `raven-node` имплементирует ту же
  `core.AdminAPI` локально + user-ops-only внешний API + config-pull+verify.
  Fan-out из центра переключается с голого Xray-gRPC на агенты — **смена одной
  имплементации `core.AdminAPI`, без переписывания api/syncer**. Вот ради чего
  узкий core-seam в Фазе 0 окупается.

### 15.7 Что валидировать перед B+ (открыто)

- **Язык/деплой агента.** Go (один бинарь, как Raven/exporter; переиспользуем
  apiclient) vs повторить ошибки Marzban с version-matrix panel↔node.
- **Формат signed-bundle.** minisign/cosign/age + git-tag? Кто держит ключ
  подписи (laptop/CI/offline)? Привязка к нашему vault-flow.
- **Pull-trigger.** Агент поллит git? Или Raven шлёт «обнови себя» (только
  сигнал, не контент — тогда контент всё равно из git, push-радиус не растёт)?
- **Backward-compat с A.** Нода может работать в режиме A (raw-gRPC) ИЛИ B+
  (агент) — конфиг-флаг `node.mode`, чтобы мигрировать ноды по одной.
- **Стоимость.** Новый repo `raven-node` (AGPL, как остальные), CI, release,
  Ansible-роль, integration-тесты агент↔Xray. Оценка: B+ ≈ 3-4 недели против
  ~1-1.5 недели на A+rsync. Решение — функция от того, насколько автономия и
  capability-confinement приоритетны против срока.
