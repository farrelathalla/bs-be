-- Migration 005: Scenario support with 2-section CSV format (Bucket + Cashflow Assumption)

-- Scenario bucket configs: the detailed bucket percentages with criteria matching
CREATE TABLE IF NOT EXISTS scenario_bucket_configs (
    id BIGSERIAL PRIMARY KEY,
    behaviour_id BIGINT REFERENCES behaviours(id) ON DELETE CASCADE,
    bucket_type VARCHAR(20) NOT NULL,      -- LCR, NSFR, IRRBB
    bucket_name VARCHAR(100) NOT NULL,
    percentage NUMERIC(10,6) DEFAULT 0,
    product_type VARCHAR(50),              -- nullable = applies to all
    ccy VARCHAR(20),                       -- nullable = applies to all
    segment VARCHAR(50),                   -- nullable = applies to all
    transactional VARCHAR(50),             -- nullable = applies to all
    value_type VARCHAR(50) DEFAULT 'Outstanding'  -- Outstanding or Market
);

-- Scenario cashflow assumptions: multiplier per criteria + bucket type
CREATE TABLE IF NOT EXISTS scenario_cashflow_assumptions (
    id BIGSERIAL PRIMARY KEY,
    behaviour_id BIGINT REFERENCES behaviours(id) ON DELETE CASCADE,
    product_type VARCHAR(50),
    ccy VARCHAR(20),
    segment VARCHAR(50),
    transactional VARCHAR(50),
    bucket_type VARCHAR(20) NOT NULL,
    percentage NUMERIC(10,6) DEFAULT 1.0
);

-- Indexes
CREATE INDEX IF NOT EXISTS idx_scenario_bucket_configs_behaviour ON scenario_bucket_configs(behaviour_id);
CREATE INDEX IF NOT EXISTS idx_scenario_cashflow_assumptions_behaviour ON scenario_cashflow_assumptions(behaviour_id);

-- Add is_scenario flag to behaviours to distinguish old-style (simple) vs new-style (2-section)
ALTER TABLE behaviours ADD COLUMN IF NOT EXISTS is_scenario BOOLEAN DEFAULT FALSE;
