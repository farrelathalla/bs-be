-- Users table
CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    username VARCHAR(100) UNIQUE NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    created_at TIMESTAMP DEFAULT NOW()
);

-- Uploads table (history)
CREATE TABLE IF NOT EXISTS uploads (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID REFERENCES users(id),
    filename VARCHAR(255) NOT NULL,
    uploaded_at TIMESTAMP DEFAULT NOW(),
    total_rows INT NOT NULL DEFAULT 0,
    status VARCHAR(20) DEFAULT 'processing',
    error_message TEXT,
    file_path VARCHAR(500) NOT NULL
);

-- Loan inputs table (raw CSV data per upload)
CREATE TABLE IF NOT EXISTS loan_inputs (
    id BIGSERIAL PRIMARY KEY,
    upload_id UUID REFERENCES uploads(id) ON DELETE CASCADE,
    row_number INT NOT NULL,
    reporting_date DATE,
    account_id VARCHAR(100),
    ccy VARCHAR(10),
    outstanding NUMERIC(20,2),
    interest_rate NUMERIC(10,8),
    start_date DATE,
    end_date DATE,
    installment_frequency INT,
    product_type VARCHAR(100),
    segment VARCHAR(100),
    daerah VARCHAR(200),
    kode_pos VARCHAR(20),
    insured_or_uninsured VARCHAR(50),
    transactional_or_non VARCHAR(50),
    method VARCHAR(20),
    interest_payment_frequency INT,
    day_count VARCHAR(20)
);

-- Cashflow results table
CREATE TABLE IF NOT EXISTS cashflow_results (
    id BIGSERIAL PRIMARY KEY,
    upload_id UUID REFERENCES uploads(id) ON DELETE CASCADE,
    loan_input_id BIGINT REFERENCES loan_inputs(id) ON DELETE CASCADE,
    remaining_days INT,
    irrbb_principal JSONB DEFAULT '{}',
    irrbb_interest JSONB DEFAULT '{}',
    lcr_principal JSONB DEFAULT '{}',
    lcr_interest JSONB DEFAULT '{}',
    nsfr_principal JSONB DEFAULT '{}',
    nsfr_interest JSONB DEFAULT '{}'
);

-- Sessions table for auth
CREATE TABLE IF NOT EXISTS sessions (
    id BIGSERIAL PRIMARY KEY,
    token VARCHAR(255) UNIQUE NOT NULL,
    user_id UUID REFERENCES users(id) ON DELETE CASCADE,
    username VARCHAR(100) NOT NULL,
    expires_at TIMESTAMP NOT NULL,
    created_at TIMESTAMP DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_loan_inputs_upload ON loan_inputs(upload_id);
CREATE INDEX IF NOT EXISTS idx_cashflow_results_upload ON cashflow_results(upload_id);
CREATE INDEX IF NOT EXISTS idx_cashflow_results_loan ON cashflow_results(loan_input_id);
CREATE INDEX IF NOT EXISTS idx_uploads_user ON uploads(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_token ON sessions(token);
CREATE INDEX IF NOT EXISTS idx_uploads_user ON uploads(user_id);
