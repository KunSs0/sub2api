-- Persist which outbound proxy a request actually used, so the admin usage log
-- can answer "where did this request really go?" for accounts bound to an
-- IP-management proxy (proxies table).
--
-- Semantics mirror ops_error_logs.upstream_errors proxy attribution
-- (see dev-docs/upstream-error-proxy-attribution.md): the values are an
-- event-time snapshot taken where the transport decides its route, and must
-- never be reconstructed later from accounts.proxy_id (expired-proxy fallback
-- rewrites that binding, so a later join does not describe the historical
-- request).
--
--   proxy_id / proxy_name NULL              -> not recorded (rows written
--                                              before this feature, batch
--                                              image settlement, OpenAI Live
--                                              and manual usage creation)
--   NULL / 'direct/no_proxy'                -> transport was explicitly told to
--                                              use no proxy
--   NULL / 'unknown'                        -> route cannot be proven from
--                                              event-time evidence (custom base
--                                              URL relay dials the proxy itself;
--                                              WebSocket without a managed proxy
--                                              falls back to the default client)
--   id   / name                             -> that managed proxy
--
-- Unlike the ops error events, the admin usage log also snapshots the proxy
-- endpoint (host/port only — never scheme, credentials or authorization data),
-- because the column exists to show where a request egressed. proxy_host and
-- proxy_port stay NULL unless proxy_id identifies a managed proxy.
--
-- Nullable with no default: on PostgreSQL 11+ this is a metadata-only change, so
-- it does NOT rewrite the (potentially large) usage_logs table. No index is
-- added: the column is display-only and is not filterable or sortable.
ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS proxy_id BIGINT;
ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS proxy_name VARCHAR(100);
ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS proxy_host VARCHAR(255);
ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS proxy_port INTEGER;

COMMENT ON COLUMN usage_logs.proxy_id IS
    'Managed proxy ID used by this request; NULL when unrecorded or when proxy_name is a sentinel.';
COMMENT ON COLUMN usage_logs.proxy_name IS
    'Snapshotted proxy name, or the sentinel direct/no_proxy / unknown; NULL for rows written before attribution existed.';
COMMENT ON COLUMN usage_logs.proxy_host IS
    'Snapshotted proxy endpoint host (no scheme/credentials); only set when proxy_id identifies a managed proxy.';
COMMENT ON COLUMN usage_logs.proxy_port IS
    'Snapshotted proxy endpoint port; only set when proxy_id identifies a managed proxy.';
