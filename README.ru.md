# xray-subscription (Русский)

[English version](README.md)

Сервер подписок для [Xray-core](https://github.com/XTLS/Xray-core), который:

- Генерирует **персональные клиентские конфиги Xray** из серверных inbound-конфигов
- Выдает **уникальный URL подписки** для пользователя
- Поддерживает фильтры подписки по протоколу и inbound-тегу
- Поддерживает форматы подписки для V2Box (`links.txt`, `links.b64`)
- Автоматически синхронизирует inbounds из `/etc/xray/config.d`
- Поддерживает VLESS, VMess, Trojan, Shadowsocks, SOCKS
- Поддерживает TCP/WS/gRPC/H2/KCP/QUIC/HTTPUpgrade/XHTTP
- Поддерживает TLS и REALITY (включая автодеривацию `publicKey`)
- Хранит данные в SQLite
- Имеет Admin API для пользователей, inbounds и роутинга

---

## Быстрый старт

### 1) Сборка

```bash
go mod tidy
make build
```

Бинарник: `./build/xray-subscription`

### 2) Конфиг

```bash
cp config.json.example /etc/xray-subscription/config.json
```

Пример:

```json
{
  "listen_addr": ":8080",
  "server_host": "YOUR_DOMAIN_OR_IP",
  "config_dir": "/etc/xray/config.d",
  "db_path": "/var/lib/xray-subscription/db.sqlite",
  "sync_interval_seconds": 60,
  "base_url": "http://YOUR_DOMAIN_OR_IP:8080",
  "admin_token": "CHANGE_ME",
  "balancer_strategy": "leastPing",
  "balancer_probe_url": "https://www.gstatic.com/generate_204",
  "balancer_probe_interval": "30s"
}
```

Параметры балансировки:

- `balancer_strategy`: `random`, `leastPing`, `leastLoad` (по умолчанию `leastPing`)
- `balancer_probe_url` и `balancer_probe_interval` используются для `leastPing/leastLoad`

### 3) Запуск

```bash
./build/xray-subscription -config /etc/xray-subscription/config.json
```

---

## Подписки

### Базовые URL

- Полный клиентский JSON: `GET /sub/{token}`
- JSON только для одного протокола: `GET /sub/{token}/protocol/{protocol}`
- JSON только для одного inbound-тега: `GET /sub/{token}/inbound/{inboundTag}`
- Ссылки (plain text): `GET /sub/{token}/links.txt`
- Ссылки (base64): `GET /sub/{token}/links.b64`
- Только VLESS-ссылки:
  - `GET /sub/{token}/vless`
  - `GET /sub/{token}/vless.b64`
  - `GET /sub/{token}/vless/list`
  - `GET /sub/{token}/vless/{vlessTag}`
  - `GET /sub/{token}/vless/{vlessTag}/b64`
- Быстрые ссылки по протоколам:
  - `GET /sub/{token}/vmess`
  - `GET /sub/{token}/vmess.b64`
  - `GET /sub/{token}/trojan`
  - `GET /sub/{token}/trojan.b64`
  - `GET /sub/{token}/ss`
  - `GET /sub/{token}/ss.b64`
- Ссылки только для одного протокола:
  - `GET /sub/{token}/protocol/{protocol}/links.txt`
  - `GET /sub/{token}/protocol/{protocol}/links.b64`
- Ссылки только для одного inbound-тега:
  - `GET /sub/{token}/inbound/{inboundTag}/links.txt`
  - `GET /sub/{token}/inbound/{inboundTag}/links.b64`
- Вспомогательный список ссылок: `GET /sub/{token}/links`

### Фильтры

- `?protocol=vless` (также `vmess`, `trojan`, `shadowsocks`, `socks`)
- `?inbound_tag=vless-xhttp-in-1`
- `?format=v2box` / `?format=links` / `?format=b64`
- `?profile=mobile` (удаляет из роутинга селекторы `geosite:` / `geoip:`)
- `?mobile=1` (то же, что `profile=mobile`)

Примеры:

```bash
curl "http://HOST:8080/sub/<token>"
curl "http://HOST:8080/sub/<token>/links.txt"
curl "http://HOST:8080/sub/<token>/links.b64"
curl "http://HOST:8080/sub/<token>/vless"
curl "http://HOST:8080/sub/<token>/vless.b64"
curl "http://HOST:8080/sub/<token>/vmess"
curl "http://HOST:8080/sub/<token>/trojan.b64"
curl "http://HOST:8080/sub/<token>/ss"
curl "http://HOST:8080/sub/<token>/vless/list"
curl "http://HOST:8080/sub/<token>/vless/vless-xhttp-in-1"
curl "http://HOST:8080/sub/<token>/vless/vless-xhttp-in-1/b64"
curl "http://HOST:8080/sub/<token>?format=v2box"
curl "http://HOST:8080/sub/<token>/protocol/vless"
curl "http://HOST:8080/sub/<token>/protocol/vmess/links.b64"
curl "http://HOST:8080/sub/<token>/inbound/vless-xhttp-in-1"
curl "http://HOST:8080/sub/<token>/inbound/vless-xhttp-in-1/links.b64"
curl "http://HOST:8080/sub/<token>?profile=mobile"
```

---

## Admin API (кратко)

Все `/api/*` требуют заголовок: `X-Admin-Token: <token>`.

### Пользователи

- `GET /api/users`
- `POST /api/users`
- `GET /api/users/{id}`
- `DELETE /api/users/{id}`
- `PUT /api/users/{id}/enable`
- `PUT /api/users/{id}/disable`
- `POST /api/users/{id}/token`
- `GET /api/users/{id}/clients`

### Роуты пользователя

- `GET /api/users/{id}/routes`
- `POST /api/users/{id}/routes`
- `PUT /api/users/{id}/routes`
- `PUT /api/users/{id}/routes/{index}`
- `DELETE /api/users/{id}/routes/{index}`
- `PUT /api/users/{id}/routes/id/{routeId}`
- `DELETE /api/users/{id}/routes/id/{routeId}`

### Глобальные роуты

- `GET /api/routes/global`
- `POST /api/routes/global`
- `PUT /api/routes/global`
- `DELETE /api/routes/global`

### Балансировщик (runtime override)

- `GET /api/config/balancer`
- `PUT /api/config/balancer`

### Inbounds и sync

- `GET /api/inbounds`
- `POST /api/sync`

---

## Правила роутинга (user/global)

Ограничения:

- `outboundTag`: только `direct`, `proxy`, `block`
- Должно быть хотя бы одно эффективное поле: `domain`, `ip`, `network`, `port`, `protocol`, `inboundTag`
- `type`: `field` (или пусто)

Примеры:

```bash
# Заменить все user-routes
curl -X PUT "http://HOST:8080/api/users/1/routes" \
  -H "X-Admin-Token: TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "rules": [
      {
        "id": "allow-okko",
        "outboundTag": "direct",
        "domain": ["okko.tv", "okko.sport"]
      },
      {
        "outboundTag": "block",
        "domain": ["geosite:category-ads-all"]
      }
    ]
  }'

# Добавить один global-route
curl -X POST "http://HOST:8080/api/routes/global" \
  -H "X-Admin-Token: TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "outboundTag": "proxy",
    "domain": ["geosite:ru-blocked"]
  }'

# Поменять стратегию балансировки без редактирования config.json
curl -X PUT "http://HOST:8080/api/config/balancer" \
  -H "X-Admin-Token: TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "strategy": "leastPing",
    "probe_url": "https://www.gstatic.com/generate_204",
    "probe_interval": "30s"
  }'

# Сбросить runtime override и вернуться к значениям из config.json
curl -X PUT "http://HOST:8080/api/config/balancer" \
  -H "X-Admin-Token: TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"reset": true}'
```

---

## Примечания

- В `/api/inbounds` поле `raw_config` отдается как JSON-объект/массив, если JSON валиден.
- Если `raw_config` поврежден, возвращается строка (fallback).
- `outboundTag: "proxy"` в правилах — логическая цель: генератор резолвит ее в конкретный outbound или balancer.
- Приоритет правил: **user > global > default**.
