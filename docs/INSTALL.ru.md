# Raven Subscribe — Инструкция по установке

## Требования

- **Xray-core** установлен и настроен (конфиги inbound в `/etc/xray/config.d`)
- **Debian/Ubuntu** или аналогичный Linux (инструкция для systemd)

---

## 1. Установка бинарника

### Вариант A: Скачать релиз

```bash
# AMD64 (большинство серверов)
curl -Lo xray-subscription https://github.com/AlchemyLink/Raven-subscribe/releases/latest/download/xray-subscription-linux-amd64

# ARM64 (например Raspberry Pi)
# curl -Lo xray-subscription https://github.com/AlchemyLink/Raven-subscribe/releases/latest/download/xray-subscription-linux-arm64

chmod +x xray-subscription
sudo mv xray-subscription /usr/local/bin/
```

### Вариант B: Собрать из исходников

```bash
git clone https://github.com/AlchemyLink/Raven-subscribe.git
cd Raven-subscribe
make build
sudo cp build/xray-subscription /usr/local/bin/
```

### Проверка

```bash
/usr/local/bin/xray-subscription -version
```

---

## 2. Создание пользователя и каталогов

```bash
# Создать пользователя xray, если нет (пакет Xray обычно создаёт)
sudo useradd -r -s /usr/sbin/nologin xray 2>/dev/null || true

# Каталог конфигурации
sudo mkdir -p /etc/xray-subscription

# Каталог данных (БД)
sudo mkdir -p /var/lib/xray-subscription
sudo chown xray:xray /var/lib/xray-subscription

# При file-based sync (api_user_inbound_tag без xray_api_addr):
# Raven пишет в config_dir — xray должен быть владельцем
sudo chown -R xray:xray /etc/xray/config.d
```

### Каталог Xray `config_dir`: владелец `xrayuser` и права `-rwxr-xr-x` (0755)

Если Xray работает под **`xrayuser`** (или другим пользователем, не `xray`), укажите **того же** пользователя в `User=` в `xray-subscription.service` и выставьте владельца и права:

```bash
sudo chown -R xrayuser:xrayuser /etc/xray/config.d
sudo find /etc/xray/config.d -type f -exec chmod 755 {} \;
sudo find /etc/xray/config.d -type d -exec chmod 755 {} \;
```

В **`/etc/xray-subscription/config.json`**, чтобы Raven при перезаписи JSON сохранял эти права:

```json
"xray_config_file_mode": "0755"
```

Иначе новые записи по умолчанию будут **0600** (`-rw-------`). **Внимание:** 0755 делает конфиги читаемыми для всех — включайте только если это допустимо.

---

## 3. Настройка

```bash
sudo cp config.json.example /etc/xray-subscription/config.json
sudo nano /etc/xray-subscription/config.json
```

**Минимальный конфиг:**

```json
{
  "server_host": "ваш-сервер.com",
  "admin_token": "ваш-секретный-токен",
  "base_url": "http://ваш-сервер.com:8080"
}
```

**С управлением пользователями через API** (Raven добавляет/удаляет пользователей в Xray):

```json
{
  "server_host": "ваш-сервер.com",
  "admin_token": "ваш-секретный-токен",
  "base_url": "http://ваш-сервер.com:8080",
  "api_user_inbound_tag": "vless-reality",
  "api_user_inbound_protocol": "vless",
  "api_user_inbound_port": 443
}
```

**С VLESS Encryption** (постквантовый ML-KEM-768, Xray ≥ 25.x):

Генерируем ключевую пару командой `xray vlessenc`:

```bash
# Генерация ключей:
xray vlessenc
# Вывод — две строки:
#   Сервер (decryption): mlkem768x25519plus.native/xorpub/random.600s.Pad.PrivKey.Seed
#   Клиент (encryption): mlkem768x25519plus.native/xorpub/random.0rtt.Pad.PubKey.Client
```

Серверную строку вставляем в поле `decryption` в конфиге Xray. Клиентскую строку (только публичные ключи) — в конфиг Raven:

`/etc/xray-subscription/config.json`:

```json
{
  "listen_addr": ":8080",
  "server_host": "vpn.example.com",
  "config_dir": "/etc/xray/config.d",
  "db_path": "/var/lib/xray-subscription/db.sqlite",
  "sync_interval_seconds": 60,
  "base_url": "https://vpn.example.com:8080",
  "admin_token": "a3f8c2d1e9b047fc82a1d3e6c5f092bb",
  "rate_limit_sub_per_min": 60,
  "rate_limit_admin_per_min": 30,
  "api_user_inbound_tag": "vless-reality-in",
  "xray_api_addr": "127.0.0.1:10085",
  "vless_client_encryption": {
    "vless-reality-in": "mlkem768x25519plus.native/xorpub/random.0rtt/1rtt.3Kh8A.wX9pR2mLqTzYvNcBdEsUoJfGiHkP4aA.CLT7mZ2NpRqLvXyBwJfGiHkP4aAeUsOdTcE",
    "vless-xhttp-in":   "mlkem768x25519plus.native/xorpub/random.0rtt/1rtt.3Kh8A.wX9pR2mLqTzYvNcBdEsUoJfGiHkP4aA.CLT7mZ2NpRqLvXyBwJfGiHkP4aAeUsOdTcE"
  }
}
```

> **Важно**: в `vless_client_encryption` — **только клиентская строка** (публичные ключи, её безопасно хранить в конфиге).
> Серверная строка (`decryption`) содержит приватные ключи и **никогда** не должна попадать в конфиг Raven.

Чтобы добавлять **каждого** нового пользователя **ко всем** inbound’ам, которые знает Raven, укажите `"api_user_all_inbounds": true` и не задавайте `api_user_inbound_tag` (или оставьте пустым).

**Сгенерировать надёжный admin token:**

```bash
openssl rand -hex 32
```

---

## 4. Установка systemd-сервиса

```bash
sudo cp xray-subscription.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable xray-subscription
sudo systemctl start xray-subscription
```

---

## 5. Проверка

```bash
# Статус сервиса
sudo systemctl status xray-subscription

# Проверка здоровья
curl http://localhost:8080/health

# Список пользователей (подставьте свой admin token)
curl -H "X-Admin-Token: ваш-секретный-токен" http://localhost:8080/api/users
```

---

## Решение проблем

### "No such file or directory" при запуске

Бинарник не найден в `/usr/local/bin/xray-subscription`. Установите его (шаг 1) или измените путь в unit-файле:

```bash
sudo nano /etc/systemd/system/xray-subscription.service
# Измените ExecStart= на путь к вашему бинарнику
```

### "unable to open database file (14)"

Пользователь `xray` не может писать в каталог данных:

```bash
sudo mkdir -p /var/lib/xray-subscription
sudo chown xray:xray /var/lib/xray-subscription
sudo systemctl restart xray-subscription
```

### Xray не может читать конфиги после добавления пользователей Raven

Raven и Xray должны работать под одним пользователем. Сервис использует `User=xray`. Проверьте:

```bash
# Xray запущен под xray
grep -E "^User=" /etc/systemd/system/xray.service

# config_dir принадлежит xray
sudo chown -R xray:xray /etc/xray/config.d
```

### За reverse proxy (nginx/caddy)

Установите `listen_addr` в `127.0.0.1:8080`, чтобы Raven слушал только localhost. `base_url` укажите с публичным доменом и `https://`.

### `/sub/{token}` или `/c/{token}` отвечает «no inbound clients for this user»

`/api/users` показывает записи в таблице **`users`**. **Подписка** требует хотя бы одну запись в **`user_clients`** (пользователь привязан к inbound с учётными данными).

Если в конфиге Raven заданы **`api_user_inbound_tag`** или **`api_user_all_inbounds`**, **первый** успешный запрос подписки для пользователя без `user_clients` **сам добавит** его в нужные inbound’ы (как при создании пользователя).

Это бывает, если:

1. **`api_user_all_inbounds`: `true`** — новым пользователям клиент создаётся на **каждом** inbound, который есть в БД (после синка из `config_dir`).
2. Задан **`api_user_inbound_tag`** — новым пользователям клиент создаётся только на этом inbound (игнорируется, если включён п. 1).
3. **`POST /api/users/{id}/clients`** с `{"tag":"тег-inbound"}` — добавить пользователя к inbound.
4. **Синхронизатор** — пользователи из `config_dir` получают `user_clients` при сканировании конфигов.

Если пользователей создавали через `POST /api/users` **без** `api_user_all_inbounds`, **без** `api_user_inbound_tag` и без `inbounds` в теле, у них есть токен, но подписки нет, пока не добавить клиентов (п. 3) или не включить п. 1–2 для новых.

### Пользователь снова в списке после `DELETE /api/users/{id}`

Если задан **`xray_api_addr`**, раньше клиент убирался только из **запущенного** Xray, а **JSON в `config_dir`** оставался с этим клиентом. Синхронизатор снова импортировал клиента и **создавал пользователя** в SQLite.

В актуальных версиях при удалении пользователя клиент убирается и из файлов inbound. Если на старой сборке эффект остаётся — удалите клиента вручную из `config_dir` или обновите Raven.

---

## Кастомизация клиентского конфига

Конфиг, который пользователь скачивает по ссылке подписки (`/sub/{token}`), генерируется динамически. Ниже — все точки кастомизации.

### Routing-правила

Дефолтный набор правил (применяется для всех пользователей, **в порядке применения**):

| # | Трафик | Куда |
|---|--------|------|
| 1 | BitTorrent | direct |
| 2 | `geosite:category-ads-all`, реклама, публичные трекеры | block |
| 3 | `geoip:private`, `geosite:private` | direct |
| 4 | `geosite:ru-blocked` | proxy |
| 5 | `geoip:ru-blocked` | proxy |
| 6 | `geoip:ru` | direct |
| 7 | Всё остальное | proxy |

Правила применяются по первому совпадению. Порядок важен: `ru-blocked → proxy` стоит перед `geoip:ru → direct`, поэтому заблокированные домены всегда идут через VPN, даже если их IP входит в `geoip:ru`.

`domainStrategy: IPOnDemand` — для правил по IP (строки 3, 6) Xray автоматически резолвит домен и сопоставляет IP с базой. Благодаря этому российские сайты (чьи IP входят в `geoip:ru`) идут напрямую без добавления явного правила по домену.

**Добавить или переопределить правила** можно через raven-dashboard:

- **Global Routes** (вкладка Routes → Global) — применяется ко всем пользователям
- **User Routes** (редактирование пользователя → вкладка Routes) — только для конкретного пользователя

Пользовательские правила вставляются **перед** дефолтными и имеют приоритет над ними. Поддерживаемые форматы:

```
geosite:ru-blocked      → proxy    # заблокированные в РФ домены
geosite:category-ru     → direct   # весь российский сегмент (только с runetfreedom геофайлами)
example.com             → direct   # конкретный домен
1.2.3.4/24              → block    # IP-подсеть
```

> **Примечание по `geosite:category-ru`:** тег доступен только если клиентское приложение использует runetfreedom-геофайлы (`geosite.dat` от runetfreedom). V2RayNG, NekoBox и Hiddify включают их по умолчанию. Стандартные геофайлы от v2fly/xtls этот тег не содержат.

Разрешённые действия: `proxy`, `direct`, `block`.

После изменения правил — обновить подписку в клиенте (Update subscription).

---

### DNS-серверы

По умолчанию в клиентский конфиг попадают `1.1.1.1`, `8.8.8.8`, `8.8.4.4`.

Переопределить через `config.json`:

```json
"client_dns_servers": [
  "1.1.1.1",
  "8.8.8.8"
]
```

С привязкой конкретного резолвера к доменам:

```json
"client_dns_servers": [
  "1.1.1.1",
  {
    "address": "77.88.8.8",
    "domains": ["geosite:yandex"],
    "skipFallback": true,
    "expectIPs": ["geoip:ru"]
  }
]
```

Поля объекта DNS-сервера:

| Поле | Описание |
|------|----------|
| `address` | IP или hostname резолвера |
| `domains` | Резолвить через этот сервер только эти домены |
| `skipFallback` | Не использовать как fallback для других доменов |
| `expectIPs` | Принимать только ответы с IP из этих диапазонов (защита от спуфинга) |

---

### Тип blackhole (блокировка рекламы)

По умолчанию заблокированные запросы получают фиктивный HTTP-ответ (`"type": "http"`) — браузер сразу видит ошибку. Если предпочитаете тихий drop без ответа:

```json
"client_blackhole_response": "none"
```

Допустимые значения: `"http"` (по умолчанию), `"none"`.

---

## Расположение файлов

| Путь | Назначение |
|------|------------|
| `/usr/local/bin/xray-subscription` | Бинарник |
| `/etc/xray-subscription/config.json` | Конфиг |
| `/var/lib/xray-subscription/db.sqlite` | База данных |
| `/etc/xray/config.d/` | Конфиги inbound Xray (Raven читает/пишет) |
