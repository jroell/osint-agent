-- migrate:up
-- The API-discovery moat: a curated, growing database of API endpoints
-- discovered behind public-facing UIs. Every js_endpoint_extract / wayback
-- enumerator / kiterunner-style scan writes findings here, deduped by
-- (target_apex, endpoint_url, discovery_tool). Over time this becomes the
-- proprietary asset distinguishing this OSINT platform from competitors.

CREATE TABLE IF NOT EXISTS api_endpoints_discovered (
  id              BIGSERIAL PRIMARY KEY,
  target_apex     TEXT NOT NULL,                -- normalized apex domain (e.g. "vurvey.app")
  endpoint_url    TEXT NOT NULL,                -- absolute URL or relative path
  endpoint_kind   TEXT NOT NULL,                -- 'absolute_url' | 'api_path' | 'graphql_op' | 'js_file' | 'subdomain'
  api_score       SMALLINT,                     -- 0-10 likelihood this is an API endpoint (from extraction tool)
  source_url      TEXT,                         -- where we found it (the JS bundle, wayback URL, etc.)
  discovery_tool  TEXT NOT NULL,                -- 'js_endpoint_extract' | 'wayback_endpoints' | 'kiterunner_scan' | etc.
  http_status     INTEGER,                      -- if probed
  auth_required   BOOLEAN,                      -- detected via 401/403 vs. 200 response
  auth_type       TEXT,                         -- 'bearer' | 'cookie' | 'api_key' | 'none' | 'unknown'
  response_sample TEXT,                         -- first 480 chars of response when probed
  metadata        JSONB DEFAULT '{}'::JSONB,    -- extras: graphql ops, response headers, etc.
  first_seen      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  last_verified   TIMESTAMPTZ,
  verify_count    INTEGER NOT NULL DEFAULT 0,
  UNIQUE (target_apex, endpoint_url, discovery_tool)
);

CREATE INDEX IF NOT EXISTS api_endpoints_target_idx     ON api_endpoints_discovered(target_apex);
CREATE INDEX IF NOT EXISTS api_endpoints_kind_idx       ON api_endpoints_discovered(endpoint_kind);
CREATE INDEX IF NOT EXISTS api_endpoints_score_idx      ON api_endpoints_discovered(api_score) WHERE api_score >= 5;
CREATE INDEX IF NOT EXISTS api_endpoints_last_verified  ON api_endpoints_discovered(last_verified);
CREATE INDEX IF NOT EXISTS api_endpoints_metadata_gin   ON api_endpoints_discovered USING GIN(metadata);

-- Companion: a per-target summary view that tracks coverage progress.
CREATE TABLE IF NOT EXISTS api_endpoint_coverage (
  target_apex     TEXT PRIMARY KEY,
  endpoints_total INTEGER NOT NULL DEFAULT 0,
  api_endpoints   INTEGER NOT NULL DEFAULT 0,   -- score >= 5
  graphql_ops     INTEGER NOT NULL DEFAULT 0,
  subdomains      INTEGER NOT NULL DEFAULT 0,
  potential_secrets INTEGER NOT NULL DEFAULT 0,
  tools_run       JSONB NOT NULL DEFAULT '[]'::JSONB, -- ['js_endpoint_extract', 'wayback_endpoints', ...]
  last_scan_at    TIMESTAMPTZ,
  first_scan_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- migrate:down
DROP TABLE IF EXISTS api_endpoint_coverage;
DROP TABLE IF EXISTS api_endpoints_discovered;
