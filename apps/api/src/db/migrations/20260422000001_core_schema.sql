-- migrate:up

-- tenants: one per paying entity (Phase 0 = one per user; Team tier in Phase 3 = many users per tenant)
CREATE TABLE tenants (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name          TEXT NOT NULL,
  tier          TEXT NOT NULL DEFAULT 'free' CHECK (tier IN ('free', 'hunter', 'operator')),
  stripe_customer_id    TEXT UNIQUE,
  stripe_subscription_id TEXT UNIQUE,
  credits_balance       BIGINT NOT NULL DEFAULT 100,  -- credits_balance in millicredits (100 millicredits = 1 credit = $0.01)
  -- Learning-loop scaffolding (inert until Phase 2)
  learning_bucket_b_opt_out    BOOLEAN NOT NULL DEFAULT false,
  benchmark_contribution_opt_in BOOLEAN NOT NULL DEFAULT false,
  byok_config                  JSONB,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX tenants_tier_idx ON tenants(tier);
CREATE INDEX tenants_stripe_customer_idx ON tenants(stripe_customer_id);

-- users: one row per Firebase UID; may belong to a tenant (Phase 0 = 1:1; Phase 3 Team = N:1)
CREATE TABLE users (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  firebase_uid  TEXT NOT NULL UNIQUE,
  tenant_id     UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  email         TEXT NOT NULL,
  display_name  TEXT,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  last_seen_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX users_tenant_idx ON users(tenant_id);
CREATE INDEX users_firebase_uid_idx ON users(firebase_uid);

-- credit_ledger: append-only; credits_balance on tenants is a materialized aggregate
CREATE TABLE credit_ledger (
  id            BIGSERIAL PRIMARY KEY,
  tenant_id     UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  user_id       UUID REFERENCES users(id),
  delta_millicredits BIGINT NOT NULL,        -- negative = spent; positive = granted/refilled
  reason        TEXT NOT NULL,                -- 'tool:<name>' | 'llm:<model>' | 'refill:<tier>' | 'admin'
  metadata      JSONB NOT NULL DEFAULT '{}',
  created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX credit_ledger_tenant_created_idx ON credit_ledger(tenant_id, created_at DESC);

-- migrate:down

DROP TABLE IF EXISTS credit_ledger;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS tenants;
