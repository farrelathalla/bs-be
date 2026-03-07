-- Behaviours table: stores named behaviour definitions
CREATE TABLE IF NOT EXISTS behaviours (
    id BIGSERIAL PRIMARY KEY,
    upload_id UUID REFERENCES uploads(id) ON DELETE CASCADE,
    name VARCHAR(200) NOT NULL,
    is_default BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

-- Behaviour bucket rows: the actual percentages per bucket
CREATE TABLE IF NOT EXISTS behaviour_buckets (
    id BIGSERIAL PRIMARY KEY,
    behaviour_id BIGINT REFERENCES behaviours(id) ON DELETE CASCADE,
    bucket_type VARCHAR(20) NOT NULL,  -- LCR, NSFR, IRRBB, ILAAP
    bucket_name VARCHAR(100) NOT NULL,
    percentage NUMERIC(10,6) DEFAULT 0,
    UNIQUE(behaviour_id, bucket_type, bucket_name)
);

-- Scenario mappings: map loan criteria to a behaviour
CREATE TABLE IF NOT EXISTS scenario_mappings (
    id BIGSERIAL PRIMARY KEY,
    upload_id UUID REFERENCES uploads(id) ON DELETE CASCADE,
    product_type VARCHAR(20),
    ccy VARCHAR(20),
    segment VARCHAR(20),
    transactional VARCHAR(20),
    behaviour_id BIGINT REFERENCES behaviours(id) ON DELETE CASCADE,
    created_at TIMESTAMP DEFAULT NOW()
);

-- Add result_type to cashflow_results
ALTER TABLE cashflow_results ADD COLUMN IF NOT EXISTS result_type VARCHAR(100) DEFAULT 'Normal';

-- Make end_date nullable in loan_inputs (it should already be, but ensure)
ALTER TABLE loan_inputs ALTER COLUMN end_date DROP NOT NULL;

-- Add default_behaviour flag to loan_inputs
ALTER TABLE loan_inputs ADD COLUMN IF NOT EXISTS default_behaviour BOOLEAN DEFAULT TRUE;

-- Add instrument_type to loan_inputs
ALTER TABLE loan_inputs ADD COLUMN IF NOT EXISTS instrument_type VARCHAR(50);

-- Indexes
CREATE INDEX IF NOT EXISTS idx_behaviours_upload ON behaviours(upload_id);
CREATE INDEX IF NOT EXISTS idx_behaviour_buckets_behaviour ON behaviour_buckets(behaviour_id);
CREATE INDEX IF NOT EXISTS idx_scenario_mappings_upload ON scenario_mappings(upload_id);
CREATE INDEX IF NOT EXISTS idx_cashflow_results_type ON cashflow_results(result_type);
