#!/usr/bin/env bash
# Local dev only: simulates a 2C2P backend notification end to end.
#   1. inserts a PENDING payment for INVOICE (skipped if it already exists)
#   2. signs a 2C2P PGW v4 body (JWT HS256) with TWOC2P_SECRET_KEY
#   3. POSTs it to the running API
# Usage (via make): make webhook-demo [INVOICE=INV-DEMO-0001] [AMOUNT=230.87] [RESP=0000]
# Reads TWOC2P_MERCHANT_ID, TWOC2P_SECRET_KEY, DB_DSN, APP_PORT from the environment (.env).
set -euo pipefail

INVOICE="${INVOICE:-INV-DEMO-0001}"
AMOUNT="${AMOUNT:-230.87}"   # decimal, as 2C2P sends it
RESP="${RESP:-0000}"         # 0000 = success, anything else = failed
TRANREF="${TRANREF:-TR-$INVOICE}"   # stable per invoice, so a resend is a DUPLICATE
: "${TWOC2P_MERCHANT_ID:?set it in .env}" "${TWOC2P_SECRET_KEY:?set it in .env}" "${DB_DSN:?set it in .env}"

# Database name from DB_DSN (postgres://user:pass@host:port/NAME?params).
db="${DB_DSN##*/}"; db="${db%%\?*}"

docker compose exec -T db psql -q -v ON_ERROR_STOP=1 -U app_user -d "$db" \
  -v invoice="$INVOICE" -v amount="$AMOUNT" <<'SQL'
INSERT INTO payments (id, invoice_no, amount, currency, status)
VALUES (gen_random_uuid(), :'invoice', round(:'amount'::numeric * 100)::bigint, 'THB', 'PENDING')
ON CONFLICT (invoice_no) DO NOTHING;
SQL

b64url() { openssl base64 -A | tr '+/' '-_' | tr -d '='; }
header=$(printf '{"alg":"HS256","typ":"JWT"}' | b64url)
payload=$(printf '{"merchantID":"%s","invoiceNo":"%s","amount":"%s","currencyCode":"THB","tranRef":"%s","respCode":"%s"}' \
  "$TWOC2P_MERCHANT_ID" "$INVOICE" "$AMOUNT" "$TRANREF" "$RESP" | b64url)
sig=$(printf '%s.%s' "$header" "$payload" | openssl dgst -sha256 -hmac "$TWOC2P_SECRET_KEY" -binary | b64url)

echo "POST /webhooks/2c2p invoice=$INVOICE amount=$AMOUNT respCode=$RESP"
curl -sS -X POST "localhost:${APP_PORT:-8080}/webhooks/2c2p" \
  -H 'Content-Type: application/json' -d "{\"payload\":\"$header.$payload.$sig\"}"
echo
