#!/usr/bin/env bash
set -euo pipefail
# One-time bootstrap. Requires: flyctl logged in, openssl, jq.
#
# This script is INTERACTIVE — it prompts for secrets. Do not run in CI.
# Run it locally once, then push + trigger the GH Actions deploy workflow.

# 1. Generate Ed25519 keypair for signed worker RPC
SEED_HEX=$(openssl genpkey -algorithm Ed25519 -outform PEM | openssl pkey -outform DER 2>/dev/null | tail -c 32 | xxd -c 64 -p | tr -d '\n')
# Derive public key from seed — pynacl available in py-worker venv
cd "$(dirname "$0")/../../apps/py-worker"
uv sync --no-dev >/dev/null 2>&1
PUB_HEX=$(uv run python -c "from nacl.signing import SigningKey; import sys; sk=SigningKey(bytes.fromhex('$SEED_HEX')); print(sk.verify_key.encode().hex())")
cd - >/dev/null

echo "SEED_HEX=$SEED_HEX"
echo "PUB_HEX=$PUB_HEX"

# 2. Create apps (idempotent)
for APP in osint-api osint-go-worker osint-py-worker; do
  flyctl apps create "$APP" 2>/dev/null || true
done

# 3. Set common secrets on each app (adjust as needed)
read -rp "Paste ANTHROPIC_API_KEY: " ANTHROPIC
read -rp "Paste STRIPE_SECRET_KEY: " STRIPE
read -rp "Paste FIREBASE_SERVICE_ACCOUNT_JSON path: " FB_JSON_PATH
read -rp "Paste DATABASE_URL (prod): " PG_URL
read -rp "Paste REDIS_URL (prod): " REDIS_URL

cd apps/api
flyctl secrets set \
  ANTHROPIC_API_KEY="$ANTHROPIC" \
  STRIPE_SECRET_KEY="$STRIPE" \
  STRIPE_WEBHOOK_SECRET="replace_me" \
  STRIPE_PRICE_ID_HUNTER="price_replace_me" \
  STRIPE_PRICE_ID_OPERATOR="price_replace_me" \
  FIREBASE_SERVICE_ACCOUNT_JSON="$(cat "$FB_JSON_PATH")" \
  FIREBASE_PROJECT_ID="osint-agent-prod" \
  DATABASE_URL="$PG_URL" \
  REDIS_URL="$REDIS_URL" \
  WORKER_SIGNING_KEY_HEX="$SEED_HEX" \
  GO_WORKER_URL="https://osint-go-worker.fly.dev" \
  PY_WORKER_URL="https://osint-py-worker.fly.dev" \
  --app osint-api
cd -

cd apps/go-worker
flyctl secrets set WORKER_PUBLIC_KEY_HEX="$PUB_HEX" --app osint-go-worker
cd -

cd apps/py-worker
flyctl secrets set WORKER_PUBLIC_KEY_HEX="$PUB_HEX" --app osint-py-worker
cd -

echo "✓ Fly secrets set. Now run (from repo root):"
echo "  cd apps/go-worker && flyctl deploy --remote-only"
echo "  cd apps/py-worker && flyctl deploy --remote-only"
echo "  # The api Dockerfile pulls from packages/shared-types, so deploy from"
echo "  # repo root with --config + --build-context:"
echo "  flyctl deploy --remote-only --config apps/api/fly.toml --build-context ."
