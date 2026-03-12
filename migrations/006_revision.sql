-- Migration 006: Revision for market_value and result classification
ALTER TABLE loan_inputs ADD COLUMN IF NOT EXISTS market_value NUMERIC(20,2) DEFAULT 0;

ALTER TABLE cashflow_results ADD COLUMN IF NOT EXISTS behaviour_id BIGINT REFERENCES behaviours(id) ON DELETE CASCADE;
ALTER TABLE cashflow_results ADD COLUMN IF NOT EXISTS result_type VARCHAR(50) DEFAULT 'Base';

CREATE INDEX IF NOT EXISTS idx_cashflow_results_behaviour ON cashflow_results(behaviour_id);
CREATE INDEX IF NOT EXISTS idx_cashflow_results_type ON cashflow_results(result_type);
