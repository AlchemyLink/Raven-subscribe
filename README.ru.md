# Raven Subscribe

Языки: [English](README.md) | **Русский**

[![Built for Xray-core](https://img.shields.io/badge/Built%20for-Xray--core-blue?logo=github)](https://github.com/XTLS/Xray-core)
[![Test](https://github.com/AlchemyLink/Raven-subscribe/actions/workflows/test.yml/badge.svg)](https://github.com/AlchemyLink/Raven-subscribe/actions/workflows/test.yml)
[![Security Scan](https://github.com/AlchemyLink/Raven-subscribe/actions/workflows/security.yml/badge.svg)](https://github.com/AlchemyLink/Raven-subscribe/actions/workflows/security.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/alchemylink/raven-subscribe)](https://goreportcard.com/report/github.com/alchemylink/raven-subscribe)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

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
- **Автоматическое создание пользователей** — пользователи создаются из поля `email` в конфигах Xray без каких-либо ручных действий
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
           └─ Отдаёт ссылки подписки
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
    "sub_url": "http://ваш-сервер:8080/sub/a3f8c2..."
  }
]
```

Передайте каждому пользователю его `sub_url` — они добавляют её в VPN-клиент и готово.

---

## Конфигурация

Полный справочник `config.json`:

```json
{
  "listen_addr": ":8080",
  "server_host": "ваш-сервер.com",
  "config_dir": "/etc/xray/config.d",
  "db_path": "/var/lib/xray-subscription/db.sqlite",
  "sync_interval_seconds": 60,
  "base_url": "http://ваш-сервер.com:8080",
  "admin_token": "ваш-секретный-токен",
  "balancer_strategy": "leastPing",
  "balancer_probe_url": "https://www.gstatic.com/generate_204",
  "balancer_probe_interval": "30s"
}
```

| Поле | По умолчанию | Описание |
|---|---|---|
| `listen_addr` | `:8080` | Адрес и порт для прослушивания |
| `server_host` | — | **Обязательно.** IP или домен вашего сервера — используется в генерируемых клиентских конфигах |
| `config_dir` | `/etc/xray/config.d` | Директория с серверными конфигами Xray |
| `db_path` | `/var/lib/xray-subscription/db.sqlite` | Путь к файлу SQLite-базы данных |
| `sync_interval_seconds` | `60` | Как часто перечитывать `config_dir` (в секундах) |
| `base_url` | `http://localhost:8080` | Базовый URL для ссылок подписки, которые показываются пользователям |
| `admin_token` | — | Секретный токен для доступа к Admin API |
| `balancer_strategy` | `leastPing` | Стратегия балансировки: `leastPing`, `leastLoad`, `random` |
| `balancer_probe_url` | `https://www.gstatic.com/generate_204` | URL для измерения задержки при `leastPing` |
| `balancer_probe_interval` | `30s` | Как часто проверять задержку |

---

## Ссылки подписки

У каждого пользователя есть уникальный токен. Базовая ссылка подписки:

```
GET /sub/{token}
```

Возвращает полный клиентский конфиг Xray в формате JSON, готовый к импорту.

### Форматы

Добавьте параметры запроса или используйте готовые пути:

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
| Оптимизация для мобильных | `/sub/{token}?profile=mobile` |

### Пример: добавить подписку в V2RayNG

1. Откройте V2RayNG → нажмите **+** → **Импорт конфига из URL**
2. Вставьте: `http://ваш-сервер:8080/sub/ВАШ_ТОКЕН`
3. Нажмите **OK** — готово. Приложение само загрузит и импортирует все подключения.

### Пример: добавить подписку в NekoBox / Hiddify

Используйте тот же URL. Эти клиенты поддерживают формат Xray JSON нативно.

### Пример: получить обычные share-ссылки

```bash
curl http://ваш-сервер:8080/sub/ВАШ_ТОКЕН/links.txt
```

Вывод:
```
vless://uuid@ваш-сервер:443?type=ws&security=tls&...#vless-ws-tls
vmess://eyJ2IjoiMiIsInBzIjoidm1lc3MtdGNwIiwiYWRkIjoieW91ci1zZXJ2ZXIiLCJwb3J0IjoiODA4MCIsImlkIjoiLi4uIn0=
trojan://password@ваш-сервер:443?security=tls&...#trojan-tls
```

### Оптимизация для мобильных устройств

Когда мобильный клиент запрашивает подписку, Raven Subscribe автоматически определяет его по заголовку `User-Agent` (Android, iPhone, iPad, V2RayNG, NekoBox, V2Box) и убирает тяжёлые `geosite:`/`geoip:` селекторы из правил маршрутизации — чтобы снизить потребление памяти.

Можно также принудительно включить: `/sub/{token}?profile=mobile`

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
    "user": {"id": 1, "username": "alice", "token": "abc123", "enabled": true},
    "sub_url": "http://ваш-сервер:8080/sub/abc123"
  }
]
```

#### Создать пользователя
```bash
POST /api/users
Content-Type: application/json

{"username": "bob"}
```
```bash
curl -X POST -H "X-Admin-Token: secret" -H "Content-Type: application/json" \
  -d '{"username":"bob"}' http://localhost:8080/api/users
```
```json
{
  "user": {"id": 2, "username": "bob", "token": "xyz789", "enabled": true},
  "sub_url": "http://ваш-сервер:8080/sub/xyz789"
}
```

#### Получить пользователя
```bash
GET /api/users/{id}
```

#### Удалить пользователя
```bash
DELETE /api/users/{id}
```

#### Включить / отключить пользователя
```bash
PUT /api/users/{id}/enable
PUT /api/users/{id}/disable
```

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
