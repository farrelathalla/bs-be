-- 009: Add ILAAP bucket columns and new loan input fields

ALTER TABLE cashflow_results ADD COLUMN IF NOT EXISTS ilaap_principal JSONB DEFAULT '{}';
ALTER TABLE cashflow_results ADD COLUMN IF NOT EXISTS ilaap_interest JSONB DEFAULT '{}';

ALTER TABLE loan_inputs ADD COLUMN IF NOT EXISTS account_number VARCHAR(255) DEFAULT '';
ALTER TABLE loan_inputs ADD COLUMN IF NOT EXISTS asset_liability INTEGER DEFAULT 1;
ALTER TABLE loan_inputs ADD COLUMN IF NOT EXISTS margin NUMERIC(10,8) DEFAULT 0;
ALTER TABLE loan_inputs ADD COLUMN IF NOT EXISTS revolving_flag VARCHAR(20) DEFAULT '';
