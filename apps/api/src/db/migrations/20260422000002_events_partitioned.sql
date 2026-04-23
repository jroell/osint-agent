-- migrate:up

-- events: append-only event stream, partitioned by month.
-- Every state-changing action writes here. Feeds:
--   (Phase 1) audit trail + trajectory logging
--   (Phase 2) learning loops (hypothesis outcomes, path quality, retrieval strategy)
CREATE TABLE events (
  id            BIGSERIAL,
  tenant_id     UUID NOT NULL,
  user_id       UUID,
  event_type    TEXT NOT NULL,         -- 'tool_call' | 'auth_signup' | 'billing_checkout' | 'credit_spend' | 'credit_grant' | ...
  payload       JSONB NOT NULL,
  trace_id      TEXT,                  -- OpenTelemetry trace ID for cross-service correlation
  created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);

-- Initial partitions: current month + next 3 months
CREATE TABLE events_2026_04 PARTITION OF events
  FOR VALUES FROM ('2026-04-01') TO ('2026-05-01');
CREATE TABLE events_2026_05 PARTITION OF events
  FOR VALUES FROM ('2026-05-01') TO ('2026-06-01');
CREATE TABLE events_2026_06 PARTITION OF events
  FOR VALUES FROM ('2026-06-01') TO ('2026-07-01');
CREATE TABLE events_2026_07 PARTITION OF events
  FOR VALUES FROM ('2026-07-01') TO ('2026-08-01');

CREATE INDEX events_tenant_created_idx ON events(tenant_id, created_at DESC);
CREATE INDEX events_type_idx ON events(event_type);
CREATE INDEX events_trace_idx ON events(trace_id) WHERE trace_id IS NOT NULL;

-- migrate:down

DROP TABLE IF EXISTS events_2026_07;
DROP TABLE IF EXISTS events_2026_06;
DROP TABLE IF EXISTS events_2026_05;
DROP TABLE IF EXISTS events_2026_04;
DROP TABLE IF EXISTS events;
