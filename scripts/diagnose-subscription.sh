#!/bin/bash
# Диагностика подписки Raven-subscribe
# На сервере: ADMIN_TOKEN=xxx bash diagnose-subscription.sh
# Или: ADMIN_TOKEN=xxx HOST=http://workingtest.duckdns.org:8080 bash diagnose-subscription.sh

TOKEN="${1:-ca6fd9dd22df0014582bf6cd84b91e188524f10b3f9336d32a0576355c3eac3f}"
HOST="${HOST:-http://localhost:8080}"
ADMIN_TOKEN="${ADMIN_TOKEN:-}"

echo "=== 1. Raven config ==="
for p in /etc/xray-subscription/config.json ./config.json; do
  [ -f "$p" ] && echo "Found: $p" && cat "$p" | grep -E 'config_dir|api_user_inbound_tag|xray_api_addr' && break
done

echo ""
echo "=== 2. Config dir (default /etc/xray/config.d) ==="
CONFIG_DIR="/etc/xray/config.d"
[ -d "$CONFIG_DIR" ] && ls -la "$CONFIG_DIR" && for f in "$CONFIG_DIR"/*.json; do [ -f "$f" ] && echo "--- $f ---" && head -100 "$f"; done || echo "Dir not found"

echo ""
echo "=== 3. API inbounds ==="
[ -n "$ADMIN_TOKEN" ] && curl -s -H "X-Admin-Token: $ADMIN_TOKEN" "$HOST/api/inbounds" || echo "Set ADMIN_TOKEN"

echo ""
echo "=== 4. API users ==="
[ -n "$ADMIN_TOKEN" ] && curl -s -H "X-Admin-Token: $ADMIN_TOKEN" "$HOST/api/users" || echo "Set ADMIN_TOKEN"

echo ""
echo "=== 5. Subscription /c/$TOKEN ==="
curl -s "$HOST/c/$TOKEN"

echo ""
echo "=== 6. DB: user_clients ==="
for db in /var/lib/xray-subscription/db.sqlite ./db.sqlite; do
  if [ -f "$db" ]; then
    echo "DB: $db"
    sqlite3 "$db" "SELECT u.id, u.username, u.token FROM users u WHERE u.token='$TOKEN';" 2>/dev/null
    USER_ID=$(sqlite3 "$db" "SELECT id FROM users WHERE token='$TOKEN';" 2>/dev/null)
    [ -n "$USER_ID" ] && sqlite3 "$db" "SELECT uc.inbound_id, ib.tag, uc.enabled FROM user_clients uc JOIN inbounds ib ON ib.id=uc.inbound_id WHERE uc.user_id=$USER_ID;" 2>/dev/null
    break
  fi
done
