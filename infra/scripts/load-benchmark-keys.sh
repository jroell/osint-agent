#!/usr/bin/env bash
# Pulls API credentials needed by the benchmark suite from their canonical
# locations (~/.zshrc env, gcloud secret manager in vurvey-development, local
# HF token cache) and writes them into repo-root `.env` so the benchmark suite
# and dev stack can reference them via environment variables only — never
# directly against the source.
#
# Run:  bash infra/scripts/load-benchmark-keys.sh
#
# Idempotent. Safe to re-run.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
ENV_FILE="$ROOT/.env"
GCP_PROJECT="vurvey-development"

upsert() {
  local key="$1"
  local value="$2"
  # Skip if value is empty.
  if [[ -z "$value" ]]; then
    echo "  $key: empty, skipping"
    return
  fi
  if grep -q "^${key}=" "$ENV_FILE" 2>/dev/null; then
    # Use a temp file so we don't trip BSD vs GNU sed -i quirks.
    awk -v k="$key" -v v="$value" 'BEGIN{FS=OFS="="} $1==k{$0=k"="v} {print}' "$ENV_FILE" > "$ENV_FILE.tmp"
    mv "$ENV_FILE.tmp" "$ENV_FILE"
    echo "  $key: updated"
  else
    echo "${key}=${value}" >> "$ENV_FILE"
    echo "  $key: added"
  fi
}

touch "$ENV_FILE"

echo "→ Pulling Anthropic + OpenAI from gcloud secret manager (project=$GCP_PROJECT)..."
ANTHROPIC=$(gcloud secrets versions access latest --secret=anthropic-api-key --project="$GCP_PROJECT" 2>/dev/null || true)
upsert "ANTHROPIC_API_KEY" "$ANTHROPIC"

OPENAI=$(gcloud secrets versions access latest --secret=openai-api-key --project="$GCP_PROJECT" 2>/dev/null || true)
upsert "OPENAI_API_KEY" "$OPENAI"

echo "→ Pulling Gemini + OpenRouter from current shell env..."
upsert "GEMINI_API_KEY" "${GEMINI_API_KEY:-}"
upsert "OPEN_ROUTER_API_KEY" "${OPEN_ROUTER_API_KEY:-}"
upsert "OPENROUTER_API_KEY" "${OPEN_ROUTER_API_KEY:-}" # alias for SDKs that expect this name

echo "→ Pulling HuggingFace token from ~/.huggingface/token..."
if [[ -f "$HOME/.huggingface/token" ]]; then
  HF=$(tr -d '\n' < "$HOME/.huggingface/token")
  upsert "HF_TOKEN" "$HF"
  upsert "HUGGINGFACE_HUB_TOKEN" "$HF"
fi

echo
echo "✓ wrote to $ENV_FILE"
echo "  keys present:"
grep -E "^(ANTHROPIC|OPENAI|GEMINI|OPEN_ROUTER|OPENROUTER|HF|HUGGINGFACE)" "$ENV_FILE" | sed 's/=.*/=<REDACTED>/'
