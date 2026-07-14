-- GLM-5.2 V0.2.1 auditable usage ledger (MariaDB 10.5+).
-- Apply to a backup/test database first. New application code remains
-- backward compatible while these nullable/defaulted columns are added.

ALTER TABLE usage_logs
  ADD COLUMN IF NOT EXISTS requested_model VARCHAR(100) NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS actual_model VARCHAR(150) NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS upstream_provider VARCHAR(100) NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS policy_version VARCHAR(50) NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS cache_write_tokens BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS reasoning_tokens BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS visible_output_tokens BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS completion_tokens BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS upstream_reported_cost DOUBLE NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS upstream_cost_currency VARCHAR(32) NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS cost_amount DOUBLE NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS cost_currency VARCHAR(32) NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS cost_source VARCHAR(32) NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS pricing_snapshot_id VARCHAR(100) NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS local_response_cache_hit TINYINT(1) NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS singleflight_shared TINYINT(1) NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS session_id_hash_prefix VARCHAR(24) NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS terminal_state VARCHAR(50) NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS finish_reason VARCHAR(50) NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS retry_count INT NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS breaker_state VARCHAR(24) NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS ttft_ms BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS pre_first_byte_failure TINYINT(1) NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS stream_interrupted TINYINT(1) NOT NULL DEFAULT 0;

ALTER TABLE model_pricings
  ADD COLUMN IF NOT EXISTS input_cache_write_price DOUBLE NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS reasoning_price DOUBLE NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS currency VARCHAR(16) NOT NULL DEFAULT 'CNY',
  ADD COLUMN IF NOT EXISTS snapshot_id VARCHAR(100) NOT NULL DEFAULT '';

-- Sanitized historical backfill. Reasoning/cache-write details did not exist
-- in legacy rows, so output is classified as visible and cache reads use the
-- conservative full input price. The source label makes this limitation
-- explicit and keeps these rows separate from upstream-reported costs.
UPDATE usage_logs
SET requested_model = CASE WHEN requested_model = '' THEN model ELSE requested_model END,
    actual_model = CASE WHEN actual_model = '' THEN model ELSE actual_model END,
    policy_version = CASE WHEN policy_version = '' THEN 'legacy-backfill-v021' ELSE policy_version END,
    completion_tokens = output_tokens,
    visible_output_tokens = output_tokens,
    cost_amount = (input_tokens / 1000.0 * 0.008) + (output_tokens / 1000.0 * 0.028),
    estimated_cost = (input_tokens / 1000.0 * 0.008) + (output_tokens / 1000.0 * 0.028),
    cost_currency = 'CNY',
    cost_source = 'pricing_snapshot',
    pricing_snapshot_id = 'glm52-cny-baseline-v021'
WHERE LOWER(model) LIKE '%glm-5.2%'
  AND upstream_reported_cost = 0;

-- Verification queries for the handoff report. They contain no prompt data.
SELECT COUNT(*) AS invalid_token_breakdowns
FROM usage_logs
WHERE LOWER(model) LIKE '%glm-5.2%'
  AND (cached_tokens > input_tokens OR reasoning_tokens > completion_tokens);

SELECT cost_source, cost_currency, pricing_snapshot_id, COUNT(*) AS rows_count,
       SUM(cost_amount) AS total_cost
FROM usage_logs
WHERE LOWER(model) LIKE '%glm-5.2%'
GROUP BY cost_source, cost_currency, pricing_snapshot_id;
