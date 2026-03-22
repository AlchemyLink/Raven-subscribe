# Raven Subscribe

Языки: [English](README.md) | **Русский**

[![Built for Xray-core](https://img.shields.io/badge/Built%20for-Xray--core-blue?logo=github)](https://github.com/XTLS/Xray-core)
[![Test](https://github.com/AlchemyLink/Raven-subscribe/actions/workflows/test.yml/badge.svg)](https://github.com/AlchemyLink/Raven-subscribe/actions/workflows/test.yml)
[![Security Scan](https://github.com/AlchemyLink/Raven-subscribe/actions/workflows/security.yml/badge.svg)](https://github.com/AlchemyLink/Raven-subscribe/actions/workflows/security.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/alchemylink/raven-subscribe)](https://goreportcard.com/report/github.com/alchemylink/raven-subscribe)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Stars](https://img.shields.io/github/stars/AlchemyLink/Raven-subscribe?style=flat)](https://github.com/AlchemyLink/Raven-subscribe/stargazers)
[![Forks](https://img.shields.io/github/forks/AlchemyLink/Raven-subscribe?style=flat)](https://github.com/AlchemyLink/Raven-subscribe/network/members)
[![Hits](https://hits.dwyl.com/AlchemyLink/Raven-subscribe.svg?style=flat)](https://hits.dwyl.com/AlchemyLink/Raven-subscribe)

**Сервер подписок для [XTLS/Xray-core](https://github.com/XTLS/Xray-core).** Читает конфиги вашего Xray-сервера, автоматически находит пользователей и выдаёт каждому персональную ссылку-подписку — чтобы VPN-клиент всегда получал актуальные настройки подключения.

---

## Содержание

- [Что это и зачем](#что-это-и-зачем)
- [Возможности](#возможности)
- [Как это работает](#как-это-работает)
- [Быстрый старт](#быстрый-старт)
- [Конфигурация](#конфигурация)
- [Ссылки подписки](#ссылки-подписки)
- [Admin API](#admin-api)
- [Правила маршрутизации](#правила-маршрутизации)
- [Протоколы и транспорты](#протоколы-и-транспорты)
- [Docker](#docker)
- [Участие в разработке](#участие-в-разработке)

---

## Что это и зачем

Когда вы запускаете Xray-сервер, каждому пользователю нужен клиентский конфиг — с адресом сервера, портом, UUID и настройками транспорта. Обновлять эти конфиги вручную при каждом изменении неудобно.

**Raven Subscribe** решает эту задачу:

1. Читает конфиги Xray-сервера из `/etc/xray/config.d`
2. Автоматически находит всех пользователей (клиентов) из этих конфигов
3. Генерирует готовый клиентский конфиг для каждого пользователя
4. Отдаёт его по уникальной ссылке-подписке

Пользователь просто добавляет свою ссылку в любой совместимый клиент (V2RayNG, NekoBox, V2Box, Hiddify и другие) — и клиент сам подтягивает актуальные настройки.

---

## Возможности

### Для пользователей
- **Персональная ссылка подписки** — одна ссылка, которая всегда возвращает актуальный конфиг
- **Несколько форматов** — полный Xray JSON, обычные share-ссылки, Base64-кодировка
- **Ссылки по протоколам** — только VLESS, только VMess, только Trojan или Shadowsocks
- **Оптимизация для мобильных** — автоматически определяется по User-Agent (Android, iPhone, NekoBox, V2RayNG) или через `?profile=mobile`
- **Персональные правила маршрутизации** — каждый пользователь может иметь свои правила: какие сайты открывать напрямую, через прокси или блокировать

### Для администраторов
- **БД как источник правды** — при заданном `api_user_inbound_tag` добавление, удаление, включение и отключение пользователей через API сразу синхронизируется с Xray
- **Автоматическое создание пользователей** — пользователи также могут создаваться из поля `email` в конфигах Xray
- **Отслеживание изменений файлов** — сервис мгновенно реагирует на изменения в `config.d` (fsnotify + периодический опрос)
- **Полноценный REST API** — управление пользователями, токенами, правилами маршрутизации и балансировщиком
- **Управление доступом** — включить/отключить доступ пользователя к конкретным inbound-подключениям
- **Ротация токенов** — сгенерировать новый токен подписки без остановки сервиса
- **Балансировщик нагрузки** — автоматическое распределение между несколькими outbound (leastPing, leastLoad, random)
- **Глобальные правила маршрутизации** — применяются сразу ко всем пользователям

### Протоколы и транспорты
- **VLESS**, **VMess**, **Trojan**, **Shadowsocks**, **SOCKS**
- **TCP**, **WebSocket**, **gRPC**, **HTTP/2**, **KCP**, **QUIC**, **HTTPUpgrade**, **XHTTP (SplitHTTP)**
- **TLS** и **REALITY** с автоматическим выводом публичного ключа

---

## Как это работает

```
/etc/xray/config.d/
    ├── vless-reality.json   ← серверные конфиги Xray
    ├── vmess-ws.json
    └── trojan-tls.json
           │
           ▼
    Raven Subscribe
    (следит за изменениями)
           │
           ├─ Парсит inbound-ы, находит пользователей
           ├─ Сохраняет в SQLite
           ├─ Отдаёт ссылки подписки
           └─ Пользователи через API → Xray (файлы или gRPC API)
                      │
                      ▼
    https://ваш-сервер.com/sub/{token}
                      │
                      ▼
    V2RayNG / NekoBox / Hiddify / V2Box
    (автоматически получает конфиг)
```

Каждый пользователь получает уникальный токен. Когда его клиент обращается по ссылке подписки, Raven Subscribe собирает полный клиентский конфиг на лету — со всеми включёнными inbound-ами, правилами маршрутизации, настройками DNS и балансировщика.

---

## Быстрый старт

### 1. Установка

**Готовый бинарь:**
```bash
curl -Lo xray-subscription https://github.com/AlchemyLink/Raven-subscribe/releases/latest/download/xray-subscription-linux-amd64
chmod +x xray-subscription
sudo mv xray-subscription /usr/local/bin/
```

**Из исходников:**
```bash
git clone https://github.com/AlchemyLink/Raven-subscribe.git
cd Raven-subscribe
make build
sudo cp build/xray-subscription /usr/local/bin/
```

### 2. Конфигурация

```bash
sudo mkdir -p /etc/xray-subscription
sudo cp config.json.example /etc/xray-subscription/config.json
sudo nano /etc/xray-subscription/config.json
```

Минимально необходимые настройки:

```json
{
  "server_host": "ваш-ip-или-домен",
  "admin_token": "ваш-секретный-токен",
  "base_url": "http://ваш-ip-или-домен:8080"
}
```

### 3. Запуск

```bash
xray-subscription -config /etc/xray-subscription/config.json
```

**Как systemd-сервис:**
```bash
sudo cp xray-subscription.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now xray-subscription
```

Сервис запускается под `User=xray`, чтобы Raven и Xray использовали одного владельца конфигов. При заданном `api_user_inbound_tag` Raven пишет в `config_dir`; Xray должен читать эти файлы. Выполните:

```bash
# Создать пользователя xray, если нет (пакет Xray обычно создаёт)
sudo useradd -r -s /usr/sbin/nologin xray 2>/dev/null || true

# Дать xray владение config_dir и данными при file-based sync
sudo chown -R xray:xray /etc/xray/config.d /var/lib/xray-subscription
```

### 4. Получить ссылки подписки для пользователей

```bash
curl -H "X-Admin-Token: ваш-секретный-токен" http://localhost:8080/api/users
```

Ответ:
```json
[
  {
    "user": {
      "id": 1,
      "username": "alice",
      "token": "a3f8c2...",
      "enabled": true
    },
    "sub_url": "http://ваш-сервер:8080/sub/a3f8c2...",
    "sub_urls": {
      "full":        "http://ваш-сервер:8080/sub/a3f8c2...",
      "links_txt":   "http://ваш-сервер:8080/sub/a3f8c2.../links.txt",
      "links_b64":   "http://ваш-сервер:8080/sub/a3f8c2.../links.b64",
      "compact":     "http://ваш-сервер:8080/c/a3f8c2...",
      "compact_txt": "http://ваш-сервер:8080/c/a3f8c2.../links.txt",
      "compact_b64": "http://ваш-сервер:8080/c/a3f8c2.../links.b64"
    }
  }
]
```

Передайте каждому пользователю его `sub_urls.compact` — они добавляют её в VPN-клиент и готово.

---

## Конфигурация

Конфигурация загружается из JSON-файла (по умолчанию: `config.json` в текущей директории). Путь можно задать флагом `-config`:

```bash
xray-subscription -config /etc/xray-subscription/config.json
```

### Full config reference

```json
{
  "listen_addr": ":8080",
  "server_host": "your-server.com",
  "config_dir": "/etc/xray/config.d",
  "db_path": "/var/lib/xray-subscription/db.sqlite",
  "sync_interval_seconds": 60,
  "base_url": "http://your-server.com:8080",
  "admin_token": "your-secret-token",
  "balancer_strategy": "leastPing",
  "balancer_probe_url": "https://www.gstatic.com/generate_204",
  "balancer_probe_interval": "30s",
  "socks_inbound_port": 2080,
  "http_inbound_port": 1081,
  "rate_limit_sub_per_min": 60,
  "rate_limit_admin_per_min": 30,
  "api_user_inbound_tag": "vless-reality",
  "xray_api_addr": ""
}
```

### Описание параметров

#### Сервер

| Поле | По умолчанию | Описание |
|------|--------------|----------|
| `listen_addr` | `:8080` | Адрес и порт для прослушивания. `:8080` — все интерфейсы, `127.0.0.1:8080` — только localhost (например, за nginx). |
| `server_host` | — | **Обязательно.** IP или домен сервера. Используется как адрес исходящих подключений в генерируемых клиентских конфигах. |
| `base_url` | `http://localhost:8080` | Полный базовый URL для ссылок подписки. Показывается пользователям в ответах API. Используйте `https://` при работе за TLS reverse proxy. |

#### Хранилище и синхронизация

| Поле | По умолчанию | Описание |
|------|--------------|----------|
| `config_dir` | `/etc/xray/config.d` | Директория с JSON-конфигами inbound-ов Xray. Raven следит за изменениями (fsnotify + периодический опрос). |
| `db_path` | `/var/lib/xray-subscription/db.sqlite` | Путь к файлу SQLite. Хранит пользователей, токены, правила маршрутизации и синхронизированные данные. |
| `sync_interval_seconds` | `60` | Интервал (секунды) пересканирования `config_dir`. Также срабатывает при изменении файлов. |

#### Admin API

| Поле | По умолчанию | Описание |
|------|--------------|----------|
| `admin_token` | — | **Обязательно.** Секретный токен для Admin API. Передаётся в заголовке `X-Admin-Token`. Используйте длинную случайную строку: `openssl rand -hex 32`. |

#### Балансировщик нагрузки

Используется, когда в конфиге Xray несколько outbound-ов (несколько прокси-нод). Определяет, как клиент выбирает между ними.

| Поле | По умолчанию | Описание |
|------|--------------|----------|
| `balancer_strategy` | `leastPing` | Стратегия: `leastPing` (минимальная задержка), `leastLoad` (меньше подключений), `random`, `roundRobin`. |
| `balancer_probe_url` | `https://www.gstatic.com/generate_204` | URL для проверки задержки (при `leastPing`). Должен быть доступен с сервера. |
| `balancer_probe_interval` | `30s` | Как часто проверять outbound-ы. Go duration: `30s`, `1m` и т.д. |

#### Генерация клиентских конфигов

| Поле | По умолчанию | Описание |
|------|--------------|----------|
| `socks_inbound_port` | `2080` | Порт локального SOCKS5-прокси в генерируемых конфигах. Используется клиентами для системного/прикладного прокси. |
| `http_inbound_port` | `1081` | Порт локального HTTP-прокси в генерируемых конфигах. |

#### Ограничение частоты запросов

Ограничивает число запросов с одного IP в минуту. `0` = отключено. Защита от злоупотреблений.

| Поле | По умолчанию | Описание |
|------|--------------|----------|
| `rate_limit_sub_per_min` | `0` | Макс. запросов/мин с IP для `/sub/*` и `/c/*`. Рекомендуется: 60 для продакшена. |
| `rate_limit_admin_per_min` | `0` | Макс. запросов/мин с IP для `/api/*`. Рекомендуется: 30. |
| `api_user_inbound_tag` | `""` | Если задан, БД — источник правды: пользователи из API добавляются в этот inbound Xray; удалённые — удаляются; включение/отключение синхронизируется с Xray. Запись в файлы `config_dir` или через Xray API (если задан `xray_api_addr`). |
| `xray_api_addr` | `""` | Если задан, пользователи синхронизируются через gRPC API Xray вместо записи в файлы. Например `127.0.0.1:8080`. Требует `api_user_inbound_tag`. В Xray должен быть включён API с HandlerService. |
| `api_user_inbound_protocol` | `""` | Запасной вариант, когда в `config_dir` нет inbound: протокол (`vless`, `vmess`, `trojan`, `shadowsocks`) для создания inbound в БД. Используйте, если конфиги Xray в другом месте. |
| `api_user_inbound_port` | `443` | Порт inbound при использовании `api_user_inbound_protocol`. |
| `xray_config_file_mode` | *(не задавать)* | Права (octal) для JSON-файлов, которые Raven пишет в `config_dir` (например `"0644"`, чтобы другой локальный пользователь мог читать конфиги при тестах). По умолчанию **`0600`**. Только биты `0`–`7` (не больше `0777`). |

**Синхронизация БД ↔ Xray** (при заданном `api_user_inbound_tag`): База данных — источник правды. Все изменения сразу отражаются в Xray:

| Действие | БД | Xray |
|----------|----|------|
| Создание (`POST /api/users`) | Добавить | Добавить в inbound |
| Удаление (`DELETE /api/users/{id}`) | Удалить | Удалить из inbound |
| Отключение (`PUT /api/users/{id}/disable`) | `enabled=false` | Удалить из inbound |
| Включение (`PUT /api/users/{id}/enable`) | `enabled=true` | Добавить в inbound |

**Режим Xray API** (при заданном `xray_api_addr`): Синхронизация через gRPC вместо конфиг-файлов. В Xray должен быть включён API с `HandlerService` в `services`.

- **Восстановление при старте:** Raven восстанавливает всех пользователей из БД в Xray через API (сохраняется при перезапуске Xray).
- **Периодическая синхронизация БД→конфиг:** Raven периодически записывает пользователей в конфиг-файлы для сохранности при перезапуске Raven и Xray.

### Пример: минимальный конфиг

```json
{
  "server_host": "vpn.example.com",
  "admin_token": "ваш-секретный-токен",
  "base_url": "https://vpn.example.com"
}
```

Остальные параметры берутся по умолчанию.

### Пример: продакшен с rate limit

```json
{
  "listen_addr": "127.0.0.1:8080",
  "server_host": "vpn.example.com",
  "base_url": "https://vpn.example.com",
  "admin_token": "ваш-секретный-токен",
  "rate_limit_sub_per_min": 60,
  "rate_limit_admin_per_min": 30
}
```

Используйте `127.0.0.1`, если Raven работает за nginx/caddy как reverse proxy.

---

## Ссылки подписки

У каждого пользователя есть два эндпоинта подписки:

| Эндпоинт | Описание |
|---|---|
| `/c/{token}` | **Основной.** Лёгкий конфиг — `geosite:`/`geoip:` селекторы убраны. Работает на всех устройствах. |
| `/sub/{token}` | Полный конфиг со всеми правилами маршрутизации включая geo-базы. |

### `/c/{token}` — основной эндпоинт (рекомендуется)

Это рекомендуемая ссылка для передачи пользователям. Возвращает полный клиентский конфиг Xray с оптимизированными правилами маршрутизации — `geosite:` и `geoip:` селекторы убраны, остаются только явные правила по доменам и IP. Это снижает потребление памяти на устройстве.

Работает на всех клиентах: V2RayNG, NekoBox, V2Box, Hiddify и десктопных клиентах.

| Что нужно | URL |
|---|---|
| Полный Xray JSON конфиг | `/c/{token}` |
| Все share-ссылки (обычный текст) | `/c/{token}/links.txt` |
| Все share-ссылки (Base64) | `/c/{token}/links.b64` |

### `/sub/{token}` — полный эндпоинт

Возвращает конфиг с полными `geosite:` и `geoip:` правилами маршрутизации. Используйте, если ваш клиент поддерживает geo-базы и нужен полный контроль над маршрутизацией.

| Что нужно | URL |
|---|---|
| Полный Xray JSON конфиг | `/sub/{token}` |
| Все share-ссылки (обычный текст) | `/sub/{token}/links.txt` |
| Все share-ссылки (Base64) | `/sub/{token}/links.b64` |
| Только VLESS | `/sub/{token}/vless` |
| Только VMess | `/sub/{token}/vmess` |
| Только Trojan | `/sub/{token}/trojan` |
| Только Shadowsocks | `/sub/{token}/ss` |
| Конкретный inbound по тегу | `/sub/{token}/inbound/{tag}` |
| Лёгкий конфиг (явно) | `/sub/{token}?profile=mobile` |

### Пример: добавить подписку в V2RayNG

1. Откройте V2RayNG → нажмите **+** → **Импорт конфига из URL**
2. Вставьте: `http://ваш-сервер:8080/c/ВАШ_ТОКЕН`
3. Нажмите **OK** — готово. Приложение само загрузит и импортирует все подключения.

### Пример: добавить подписку в NekoBox / Hiddify

Используйте тот же `/c/{token}`. Эти клиенты поддерживают формат Xray JSON нативно.

### Пример: получить обычные share-ссылки

```bash
curl http://ваш-сервер:8080/c/ВАШ_ТОКЕН/links.txt
```

Вывод:
```
vless://uuid@ваш-сервер:443?type=ws&security=tls&...#vless-ws-tls
vmess://eyJ2IjoiMiIsInBzIjoidm1lc3MtdGNwIiwiYWRkIjoieW91ci1zZXJ2ZXIiLCJwb3J0IjoiODA4MCIsImlkIjoiLi4uIn0=
trojan://password@ваш-сервер:443?security=tls&...#trojan-tls
```

### Автоопределение устройства

Когда мобильный клиент запрашивает `/sub/{token}`, Raven Subscribe автоматически определяет его по заголовку `User-Agent` (Android, iPhone, iPad, V2RayNG, NekoBox, V2Box) и применяет лёгкий профиль автоматически. Эндпоинт `/c/{token}` всегда использует лёгкий профиль независимо от User-Agent.

---

## Admin API

Все admin-эндпоинты требуют аутентификации. Передайте `admin_token` в заголовке или параметре запроса:

```bash
# В заголовке (рекомендуется)
curl -H "X-Admin-Token: ваш-секретный-токен" http://localhost:8080/api/users

# В параметре запроса
curl "http://localhost:8080/api/users?admin_token=ваш-секретный-токен"
```

### Пользователи

#### Список всех пользователей
```bash
GET /api/users
```
```bash
curl -H "X-Admin-Token: secret" http://localhost:8080/api/users
```
```json
[
  {
    "user": {"id": 1, "username": "alice@example.com", "token": "abc123", "enabled": true},
    "sub_url": "http://ваш-сервер:8080/sub/abc123",
    "sub_urls": {
      "full":        "http://ваш-сервер:8080/sub/abc123",
      "links_txt":   "http://ваш-сервер:8080/sub/abc123/links.txt",
      "links_b64":   "http://ваш-сервер:8080/sub/abc123/links.b64",
      "compact":     "http://ваш-сервер:8080/c/abc123",
      "compact_txt": "http://ваш-сервер:8080/c/abc123/links.txt",
      "compact_b64": "http://ваш-сервер:8080/c/abc123/links.b64"
    }
  }
]
```

#### Создать пользователя
```bash
POST /api/users
Content-Type: application/json

{"username": "bob"}
```
При создании поле `email` не передаётся; внутри оно совпадает с `username` для Xray. В JSON API поля `email` нет (используйте `username`).

```bash
curl -X POST -H "X-Admin-Token: secret" -H "Content-Type: application/json" \
  -d '{"username":"bob"}' http://localhost:8080/api/users
```
```json
{
  "user": {"id": 2, "username": "bob", "token": "xyz789", "enabled": true},
  "sub_url": "http://ваш-сервер:8080/sub/xyz789",
  "sub_urls": {
    "full":        "http://ваш-сервер:8080/sub/xyz789",
    "links_txt":   "http://ваш-сервер:8080/sub/xyz789/links.txt",
    "links_b64":   "http://ваш-сервер:8080/sub/xyz789/links.b64",
    "compact":     "http://ваш-сервер:8080/c/xyz789",
    "compact_txt": "http://ваш-сервер:8080/c/xyz789/links.txt",
    "compact_b64": "http://ваш-сервер:8080/c/xyz789/links.b64"
  }
}
```
При заданном `api_user_inbound_tag` пользователь также добавляется в Xray (конфиг или API).

#### Получить пользователя
```bash
GET /api/users/{id}
```

#### Удалить пользователя
```bash
DELETE /api/users/{id}
```
`{id}` принимает **числовой id** или **username** (включая email-формат, например `alice@example.com`). Применяется к `GET`, `DELETE`, `enable`, `disable`, `token`, `routes` и `clients`.

При заданном `api_user_inbound_tag` пользователь также удаляется из Xray.

#### Пример: создать и удалить (bash)

```bash
HOST="http://localhost:8080"
ADMIN="ваш-секретный-admin-token"

# 1) Создать пользователя
CREATE_JSON=$(curl -sS -X POST "$HOST/api/users" \
  -H "X-Admin-Token: $ADMIN" \
  -H "Content-Type: application/json" \
  -d '{"username":"alice@example.com"}')
echo "$CREATE_JSON"

# 2) Удалить по username (без jq)
curl -sS -X DELETE "$HOST/api/users/alice@example.com" \
  -H "X-Admin-Token: $ADMIN"
# {"status":"deleted"}

# — или по числовому id (нужен jq)
USER_ID=$(echo "$CREATE_JSON" | jq -r '.user.id')
curl -sS -X DELETE "$HOST/api/users/$USER_ID" \
  -H "X-Admin-Token: $ADMIN"

# 3) Проверить, что пользователя нет
curl -sS -H "X-Admin-Token: $ADMIN" "$HOST/api/users/alice@example.com"
# {"error":"user not found"}
```

#### Включить / отключить пользователя
```bash
PUT /api/users/{id}/enable
PUT /api/users/{id}/disable
```
При заданном `api_user_inbound_tag` пользователь добавляется в Xray или удаляется из него соответственно.

#### Перегенерировать токен подписки
```bash
POST /api/users/{id}/token
```
Возвращает новый `{token, sub_url}`. Старая ссылка перестаёт работать немедленно.

#### Список подключений пользователя
```bash
GET /api/users/{id}/clients
```
Показывает, к каким inbound-ам подключён пользователь и включён ли каждый из них.

#### Добавить одно inbound-подключение существующему пользователю
```bash
POST /api/users/{id}/clients
Content-Type: application/json

{
  "tag": "vless-xhttp-in",
  "protocol": "vless"
}
```
Пример:
```bash
curl -H "X-Admin-Token: <admin-token>" \
  -X POST http://<host>:8080/api/users/16/clients \
  -d '{"tag":"vless-xhttp-in"}'
```
- `tag` обязателен.
- `protocol` опционален. Если не передан, определяется по `tag` из синхронизированных inbound-ов, затем используется fallback `api_user_inbound_protocol`.
- Если пользователь уже подключен к этому inbound, возвращается существующая запись клиента (идемпотентно).

#### Включить / отключить конкретное подключение
```bash
PUT /api/users/{userId}/clients/{inboundId}/enable
PUT /api/users/{userId}/clients/{inboundId}/disable
```
Используйте это, чтобы дать пользователю доступ только к определённым серверам или протоколам.

### Inbound-ы

#### Список всех синхронизированных inbound-ов
```bash
GET /api/inbounds
```
```json
[
  {
    "id": 1,
    "tag": "vless-reality",
    "protocol": "vless",
    "port": 443,
    "config_file": "/etc/xray/config.d/vless-reality.json"
  }
]
```

#### Принудительная синхронизация
```bash
POST /api/sync
```
Немедленно перечитывает `config_dir`. Полезно после редактирования конфигов Xray.

### Балансировщик

#### Получить текущие настройки
```bash
GET /api/config/balancer
```

#### Изменить настройки на лету
```bash
PUT /api/config/balancer
Content-Type: application/json

{
  "strategy": "leastPing",
  "probe_url": "https://www.gstatic.com/generate_204",
  "probe_interval": "30s"
}
```

#### Сбросить к значениям из конфига
```bash
PUT /api/config/balancer
Content-Type: application/json

{"reset": true}
```

### Проверка работоспособности
```bash
GET /health
```
```json
{"status": "ok"}
```
Аутентификация не требуется. Используйте для мониторинга доступности.

---

## Правила маршрутизации

Raven Subscribe генерирует клиентские конфиги с трёхуровневой системой маршрутизации:

```
Правила пользователя  →  Глобальные правила  →  Встроенные правила по умолчанию
(высший приоритет)                              (низший приоритет)
```

### Встроенные правила по умолчанию

Каждый генерируемый конфиг автоматически включает:

- **Напрямую**: российские сервисы (Яндекс, ВКонтакте, Lamoda и др.), локальные IP, `geoip:ru`
- **Через прокси**: `geosite:ru-blocked`, `geoip:ru-blocked`
- **Блокировать**: реклама и публичные торрент-трекеры

### Добавить глобальное правило (для всех пользователей)

```bash
POST /api/routes/global
Content-Type: application/json

{
  "type": "field",
  "outboundTag": "direct",
  "domain": ["example.com", "geosite:cn"]
}
```

### Добавить правило для конкретного пользователя

```bash
POST /api/users/{id}/routes
Content-Type: application/json

{
  "type": "field",
  "outboundTag": "block",
  "domain": ["ads.example.com"]
}
```

### Схема правила

```json
{
  "id": "необязательный-id",
  "type": "field",
  "outboundTag": "direct | proxy | block",
  "domain": ["example.com", "geosite:ru-blocked"],
  "ip": ["1.1.1.1/32", "geoip:ru"],
  "network": "tcp | udp",
  "port": "443",
  "protocol": ["http", "tls"],
  "inboundTag": ["socks"]
}
```

`outboundTag` должен быть одним из: `direct`, `proxy`, `block`.

---

## Протоколы и транспорты

### Протоколы

| Протокол | Формат share-ссылки | Примечания |
|---|---|---|
| VLESS | `vless://uuid@host:port?...#tag` | Поддерживает REALITY, TLS, plain |
| VMess | `vmess://base64(json)` | |
| Trojan | `trojan://password@host:port?...#tag` | |
| Shadowsocks | `ss://base64(method:pass)@host:port#tag` | Одиночный и мультипользовательский |
| SOCKS | — | Без share-ссылки |

### Транспортные слои

| Транспорт | Описание |
|---|---|
| TCP | Сырой TCP с опциональной HTTP-обфускацией заголовков |
| WebSocket | WS с path и host-заголовками |
| gRPC | gRPC с serviceName |
| HTTP/2 | H2 с host и path |
| mKCP | UDP-based, с типами заголовков |
| QUIC | QUIC-транспорт |
| HTTPUpgrade | HTTP upgrade handshake |
| XHTTP / SplitHTTP | Split HTTP для CDN-friendly подключений |

### Слои безопасности

| Безопасность | Примечания |
|---|---|
| TLS | Убирает серверные сертификаты, устанавливает `fingerprint: chrome` по умолчанию |
| REALITY | Автоматически выводит `publicKey` из серверного `privateKey`, берёт первый `serverName` и `shortId` |

---

## Docker

### Запуск через Docker Compose

```yaml
# docker-compose.yml
services:
  raven-subscribe:
    image: ghcr.io/alchemylink/raven-subscribe:latest
    ports:
      - "8080:8080"
    volumes:
      - ./config.json:/etc/xray-subscription/config.json:ro
      - /etc/xray/config.d:/etc/xray/config.d:ro
      - raven-data:/var/lib/xray-subscription
    restart: unless-stopped

volumes:
  raven-data:
```

```bash
docker compose up -d
```

### Сборка из исходников

```bash
docker build -t raven-subscribe .
docker run -p 8080:8080 \
  -v ./config.json:/etc/xray-subscription/config.json:ro \
  -v /etc/xray/config.d:/etc/xray/config.d:ro \
  raven-subscribe
```

---

## Участие в разработке

Смотрите [CONTRIBUTING.md](CONTRIBUTING.md).

### Перед отправкой PR

```bash
go test ./... -race
golangci-lint run --timeout=5m
```

### Релиз

```bash
make release VERSION=v1.2.3
```

---

## Лицензия

[MIT](LICENSE) © AlchemyLink
