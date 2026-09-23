-- Persist request-stage timings so usage records can explain slow TTFT without
-- requiring access to structured server logs. Historical rows remain NULL.
ALTER TABLE usage_logs
    ADD COLUMN IF NOT EXISTS timing_breakdown JSONB;
