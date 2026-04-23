function required(name: string): string {
  const v = process.env[name];
  if (!v) throw new Error(`Missing required env var: ${name}`);
  return v;
}

function optional(name: string, fallback: string): string {
  return process.env[name] ?? fallback;
}

export const config = {
  env: optional("NODE_ENV", "development"),
  port: Number(optional("PORT", "3000")),
  logLevel: optional("LOG_LEVEL", "info"),
  databaseUrl: required("DATABASE_URL"),
  redisUrl: required("REDIS_URL"),
  firebase: {
    projectId: required("FIREBASE_PROJECT_ID"),
  },
  anthropic: {
    apiKey: required("ANTHROPIC_API_KEY"),
  },
  stripe: {
    secretKey: required("STRIPE_SECRET_KEY"),
    webhookSecret: required("STRIPE_WEBHOOK_SECRET"),
    priceIdHunter: required("STRIPE_PRICE_ID_HUNTER"),
    priceIdOperator: required("STRIPE_PRICE_ID_OPERATOR"),
  },
  workers: {
    goUrl: required("GO_WORKER_URL"),
    pyUrl: required("PY_WORKER_URL"),
    signingKeyHex: required("WORKER_SIGNING_KEY_HEX"),
  },
  creditPriceUsd: Number(optional("CREDIT_PRICE_USD", "0.01")),
} as const;
