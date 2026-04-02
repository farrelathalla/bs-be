# BetterBankings Backend (bs-be) — CLAUDE.md

> Read this file completely before touching any code. It is the single source of truth for project context, architecture, and conventions.

---

## What This Project Is

**bs-be** is the Go backend for the BetterBankings cashflow analysis platform. It receives CSV or XLSX uploads of loan portfolio data, validates them, computes amortization schedules, and distributes cashflows into regulatory time buckets (IRRBB, LCR, NSFR, ILAAP). It supports default behaviours, asset/liability sign flipping, custom scenario behaviours (with 2-section CSV or 2-sheet XLSX import), pivot table aggregation, Excel export, and a SuperAdmin reference-data management panel.

The frontend is in `../betterbankings-software/` (Next.js). The original Python calculation logic is in `../installment_software/` — the Go `calculator/` package is a port of that Python code.

---

## Tech Stack

| Thing         | Value                                                                   |
| ------------- | ----------------------------------------------------------------------- |
| Language      | Go 1.22                                                                 |
| Web Framework | Gin v1.10                                                               |
| Database      | PostgreSQL 16 (via `lib/pq`)                                            |
| Auth          | Session tokens stored in `sessions` table, passwords hashed with bcrypt |
| Excel Export  | `xuri/excelize/v2`                                                      |
| Deployment    | Docker (multi-stage build) + Nginx reverse proxy with self-signed SSL   |
| Default Port  | `8002` (configurable via `PORT` env var)                                |

---

## Folder Structure

```
bs-be/
├── main.go                     Gin app, router registration, migrations, seed
├── go.mod / go.sum
├── Dockerfile                  Multi-stage Alpine build → binary + migrations
├── docker-compose.yml          db (Postgres) + api + nginx (SSL termination)
├── config/
│   ├── config.go               DB connection (retry loop × 30), env helpers, JWT secret
│   └── seed.go                 Seeds admin/superadmin users, reference data, default behaviour
├── middleware/
│   └── middleware.go           CORS, AuthMiddleware (Bearer token → session lookup), SuperAdminMiddleware
├── handlers/
│   ├── auth.go                 POST /login, /logout, GET /auth/check
│   ├── upload.go               POST /upload, GET /upload/status/:id, GET /history, DELETE /history/:id,
│   │                           GET /results/:id, POST /uploads/:id/reprocess — core processing pipeline
│   ├── behaviour.go            POST /uploads/:id/behaviours (scenario CSV import), CRUD behaviours
│   │                           + parseScenarioCSV() + overlap validator (mirrors installment_software/validator.py)
│   ├── scenario.go             CRUD /uploads/:id/mappings — map loan criteria → behaviour
│   ├── pivot.go                GET /pivot/:id — dynamic GROUP BY aggregation
│   │                           GET /results/:id/summary — summary stats
│   │                           GET /results/:id/filter-options — distinct column values
│   ├── export.go               GET /export/:id — Excel (.xlsx) download
│   ├── preset.go               CRUD /presets — user-scoped JSON config presets
│   └── reference.go            CRUD /reference/:table — superadmin-only reference data management
├── models/
│   ├── loan.go                 Loan struct (parsed from CSV/XLSX), TenorDays/TenorMonths helpers
│   │                           Fields: AccountNumber, AssetLiability, Margin, RevolvingFlag (added)
│   ├── upload.go               Upload, CashflowResult, ResultRow, PaginatedResponse, PivotGroup, ValidationError
│   ├── behaviour.go            Behaviour, BehaviourBucket, ScenarioMapping
│   └── reference.go            ReferenceItem (generic id/name pair)
├── calculator/
│   ├── schedule.go             GenerateSchedule — amortization schedule (annuity/flat/bullet)
│   ├── bucket.go               ComputeAllBuckets — IRRBB/LCR/NSFR bucketing from schedule rows
│   ├── default_behaviour.go    BehaviourWeights, ComputeBehaviourBuckets, ScenarioData,
│   │                           ComputeAllScenarioBuckets, LoadScenarioData
│   └── yearfrac.go             YearFraction (30/360, ACT/360, ACT/365), PMT, Round2
├── validator/
│   ├── csv.go                  ValidateAndParseCSV — column aliases, delimiter detection, BOM removal,
│   │                           date/number parsing (Indonesian format), ID→name mapping for Method/DayCount
│   └── xlsx.go                 ValidateAndParseXLSX — Excel file parsing via excelize, Excel date serial
│                               number handling, float-to-int conversion, interest rate kept as decimal
├── migrations/
│   ├── 001_init.sql            users, uploads, loan_inputs, cashflow_results, sessions
│   ├── 003_reference_tables.sql product_types, segments, methods, day_counts, currencies, etc.
│   ├── 004_behaviours.sql      behaviours, behaviour_buckets, scenario_mappings, result_type column
│   ├── 005_scenarios.sql       scenario_bucket_configs, scenario_cashflow_assumptions, is_scenario flag
│   ├── 006_revision.sql        behaviour_id on cashflow_results, market_value on loan_inputs
│   ├── 007_additional_refs.sql instrument_types, transactional_types, installment_frequencies
│   ├── 008_presets.sql         user_presets table
│   └── 009_ilaap_and_fields.sql ILAAP JSONB columns on cashflow_results,
│                               account_number/asset_liability/margin/revolving_flag on loan_inputs
└── nginx/
    ├── nginx.conf              SSL termination → proxy_pass http://api:8002
    └── entrypoint.sh           Self-signed cert generation on startup
```

---

## Running Locally

### With Docker Compose (recommended)

```bash
cd bs-be
docker compose up --build
# API available at https://localhost:8002
# Default users seeded: admin/admin123 (user), superadmin/admin123 (superadmin)
```

### Without Docker

Requires PostgreSQL running locally with:

- Database: `betterbankings`, User: `bb_user`, Password: `bb_secure_pass`

```bash
cd bs-be
go run .
# → http://localhost:8002
```

---

## Environment Variables

| Variable      | Default              | Description                                                                  |
| ------------- | -------------------- | ---------------------------------------------------------------------------- |
| `DB_HOST`     | `localhost`          | Postgres host                                                                |
| `DB_PORT`     | `5432`               | Postgres port                                                                |
| `DB_USER`     | `bb_user`            | Postgres user                                                                |
| `DB_PASSWORD` | `bb_secure_pass`     | Postgres password                                                            |
| `DB_NAME`     | `betterbankings`     | Postgres database name                                                       |
| `JWT_SECRET`  | `bb-secret-key-2025` | Used for... actually not JWT — token is random hex, stored in sessions table |
| `UPLOAD_DIR`  | `/var/www/uploads`   | Where uploaded CSV files are stored                                          |
| `PORT`        | `8002`               | Server listen port                                                           |

---

## Database Schema

### Core Tables

```sql
users (id UUID PK, username, password_hash, role, created_at)
sessions (id BIGSERIAL, token UNIQUE, user_id FK, username, expires_at, created_at)
uploads (id UUID PK, user_id FK, filename, uploaded_at, total_rows, status, error_message, file_path)
loan_inputs (id BIGSERIAL, upload_id FK CASCADE, row_number, reporting_date, account_id, ccy,
             outstanding NUMERIC(20,2), interest_rate NUMERIC(10,8), start_date, end_date NULLABLE,
             installment_frequency, product_type, segment, daerah, kode_pos, insured_or_uninsured,
             transactional_or_non, method, interest_payment_frequency, day_count,
             default_behaviour BOOLEAN, instrument_type, market_value,
             account_number VARCHAR(255), asset_liability INTEGER DEFAULT 1,
             margin NUMERIC(10,8) DEFAULT 0, revolving_flag VARCHAR(20) DEFAULT '')
cashflow_results (id BIGSERIAL, upload_id FK CASCADE, loan_input_id FK CASCADE, remaining_days,
                  irrbb_principal JSONB, irrbb_interest JSONB, lcr_principal JSONB, lcr_interest JSONB,
                  nsfr_principal JSONB, nsfr_interest JSONB, ilaap_principal JSONB, ilaap_interest JSONB,
                  result_type VARCHAR(100), behaviour_id FK)
```

### Behaviour / Scenario Tables

```sql
behaviours (id BIGSERIAL, upload_id FK NULLABLE CASCADE, name, is_default BOOLEAN, is_scenario BOOLEAN,
            created_at, updated_at)
behaviour_buckets (id, behaviour_id FK CASCADE, bucket_type, bucket_name, percentage NUMERIC(10,6),
                   UNIQUE(behaviour_id, bucket_type, bucket_name))
scenario_mappings (id, upload_id FK CASCADE, product_type, ccy, segment, transactional, behaviour_id FK CASCADE)
scenario_bucket_configs (id, behaviour_id FK CASCADE, bucket_type, bucket_name, percentage,
                         product_type, ccy, segment, transactional, value_type DEFAULT 'Outstanding')
scenario_cashflow_assumptions (id, behaviour_id FK CASCADE, product_type, ccy, segment, transactional,
                               bucket_type, percentage DEFAULT 1.0)
```

### Reference Tables (all share `id TEXT PK, name TEXT, updated_at` schema)

`product_types`, `segments`, `methods`, `day_counts`, `currencies`, `instrument_types`, `transactional_types`, `installment_frequencies`

### User Presets

```sql
user_presets (id BIGSERIAL, user_id FK, name, config JSONB, created_at, updated_at)
```

**Cascade deletes:** Deleting an `upload` cascades to `loan_inputs`, `cashflow_results`, `behaviours` (upload-scoped), and `scenario_mappings`.

---

## Authentication Flow

1. `POST /api/login` — username/password → bcrypt verify → generate random 32-byte hex token → store in `sessions` table (expires 24h)
2. Response: `{ token, username, user_id, role }`
3. All protected routes require `Authorization: Bearer <token>` header
4. `AuthMiddleware` looks up the token in `sessions` table (joined with `users` for `role`), sets `user_id`, `username`, `role` on the Gin context
5. `SuperAdminMiddleware` checks `role == "superadmin"` — used for reference table CRUD

**Roles:** `user` (regular), `superadmin` (can manage reference tables)

**Seeded users:** `admin/admin123` (role: user), `superadmin/admin123` (role: superadmin)

---

## Processing Pipeline — Upload → Calculate → Store

### 1. Upload (`POST /api/upload`)

- Receives multipart file (CSV, TXT, or XLSX)
- Detects file type by extension:
  - `.xlsx`/`.xls` → `validator.ValidateAndParseXLSX()` — reads first sheet via excelize
  - Otherwise → `validator.ValidateAndParseCSV()` — validates headers (with aliases), parses rows
- **CSV parsing:**
  - Handles BOM removal, delimiter auto-detection (comma, semicolon, tab)
  - Supports Indonesian number format (`1.000.000,50`)
  - Interest Rate is divided by 100 (user inputs percentage, stored as decimal)
- **XLSX parsing:**
  - Reads first sheet, normalizes headers via same alias mapping
  - Handles Excel date serial numbers and date string formats
  - Converts float strings (e.g., `"1.0"`) to integers for Method/DayCount/InstrumentType
  - Interest Rate is NOT divided by 100 (Excel stores as decimal already)
- **Common to both:**
  - Method accepts ID (`1`=annuity, `2`=flat) or string
  - DayCount accepts ID (`1`=30/360, `2`=ACT/360, `3`=ACT/365) or string
  - EndDate can be empty/NULL/NA → `nil` (no maturity loan)
- Saves file to disk, creates `uploads` record with status `processing`
- Spawns goroutine: `processLoans(uploadID, loans)`

### 2. Process Loans (background goroutine)

- Loads global default behaviour weights
- Loads upload-specific scenario behaviours
- Batches of 500 loans, each batch in a DB transaction:

**For each loan:**

**Base Result (Contractual OR Default Behaviour):**

- If `loan.HasEndDate()`: Generate amortization schedule → bucket into IRRBB/LCR/NSFR/ILAAP → result_type = `"Contractual"`
- If no end date: Apply default behaviour weights → result_type = `"Default Behaviour"`
- Sign flipping: if `AssetLiability == 2`, negate all bucket values via `negateBucketMap()`
- Insert into `cashflow_results` with `behaviour_id = NULL`

**Scenario Results (one per active scenario):**

- For each scenario: `ComputeAllScenarioBuckets()` using scenario bucket configs + cashflow assumptions
- Falls back to base result values when no matching config found
- Sign flipping applied for non-fallback scenario values when `AssetLiability == 2`
- Insert with `behaviour_id = scenario.BehaviourID`, result_type = scenario name

### 3. Reprocess (`POST /api/uploads/:id/reprocess`)

- Reads existing `loan_inputs` from DB (does NOT re-insert them — critical)
- Deletes all existing `cashflow_results` for this upload
- Re-runs the same calculation pipeline
- Prevents concurrent reprocessing via status check

---

## Calculator Engine — `calculator/` Package

### `schedule.go` — Amortization Schedule Generation

- Exact port of `installment_software/calculator.py` `Amortization.schedule()`
- Generates payment dates backward from end_date, stepping by frequency months
- Supports separate installment frequency and interest payment frequency
- Event-driven loop: processes interest accrual and principal payment at each event date
- **Methods:** annuity (PMT-based), flat (equal principal), bullet (no installment freq)
- **Day count conventions:** 30/360 (month-based), ACT/360, ACT/365

### `bucket.go` — Bucketing

- **IRRBB:** 18 time buckets from ≤1M to >20Y (day-based first bucket, month-based rest)
- **LCR:** 2 buckets (CF ≤30D, CF >30D) — interest only counted in ≤30D bucket
- **NSFR:** 3 buckets (CF <6M, CF 6M-12M, CF >12M) — interest always 0
- **ILAAP:** 41 granular buckets (No Maturity, D-1..D-30, W4<=W5, W5<=2M, monthly, yearly up to >5Y) — interest always 0
  - Uses `ilaapMonthsBetween()` with relativedelta semantics: if residual days exist beyond month boundary, month count increments by 1
  - Uses `getILAAPBucket()` for day-level classification (D-1 through D-30, then week/month/year buckets)

### `default_behaviour.go` — Behaviour & Scenario Computation

- `BehaviourWeights` = `map[string]map[string]float64` — `{bucketType: {bucketName: percentage}}`
- `ComputeBehaviourBuckets()` — outstanding × weight for each bucket, interest = 0
- `ScenarioData` — holds `BucketConfigs` and `CashflowAssumptions`
- `FindMatchingConfig()` — linear scan with wildcard matching (`"All"` = matches everything; empty string does NOT match — matches Python behaviour)
- `ComputeScenarioBucketsForType()` — `baseValue × percentage × cashflowAssumption`
  - `ValueType`: "Outstanding", "Market", or "Market Value" determines the base value
  - Market value check: `marketValue != 0` (not `> 0`, to allow negative market values)
- `ComputeAllScenarioBuckets()` — runs for IRRBB, LCR, NSFR, ILAAP; falls back to amortization/default when no matching config

### `yearfrac.go` — Financial Math

- `YearFraction()` — 30/360 (month-based), ACT/360 (days/360), ACT/365 (days/365)
- `PMT()` — standard annuity payment formula (equivalent to numpy_financial.pmt)
- `Round2()` — rounds to 2 decimal places

---

## API Endpoints

### Public

| Method | Path          | Description                    |
| ------ | ------------- | ------------------------------ |
| POST   | `/api/login`  | Username/password auth → token |
| GET    | `/api/health` | Returns `{"status":"ok"}`      |

### Protected (requires `Authorization: Bearer <token>`)

| Method | Path                               | Description                                                          |
| ------ | ---------------------------------- | -------------------------------------------------------------------- |
| GET    | `/api/auth/check`                  | Returns user_id, username, role                                      |
| POST   | `/api/auth/logout`                 | Deletes session                                                      |
| POST   | `/api/upload`                      | Upload CSV/XLSX → validate → process (background)                    |
| GET    | `/api/upload/status/:id`           | Poll processing status                                               |
| GET    | `/api/history`                     | List user's uploads                                                  |
| DELETE | `/api/history/:id`                 | Delete upload + cascaded data + file                                 |
| GET    | `/api/results/:id`                 | Paginated results (with filter_type, filters, behaviour_id, sorting) |
| GET    | `/api/results/:id/summary`         | Summary stats + bucket totals                                        |
| GET    | `/api/results/:id/filter-options`  | Distinct values for column filtering                                 |
| GET    | `/api/pivot/:id`                   | Dynamic pivot aggregation (any combination of group-by keys)         |
| GET    | `/api/export/:id`                  | Excel download with applied filters                                  |
| POST   | `/api/uploads/:id/behaviours`      | Upload scenario CSV (2-section) or XLSX (2-sheet)                    |
| GET    | `/api/uploads/:id/behaviours`      | List behaviours for upload                                           |
| GET    | `/api/behaviours/:id`              | Get behaviour with buckets                                           |
| PUT    | `/api/behaviours/:id`              | Update behaviour name / re-upload CSV                                |
| DELETE | `/api/behaviours/:id`              | Delete behaviour (cannot delete default)                             |
| GET    | `/api/uploads/:id/mappings`        | List scenario mappings                                               |
| POST   | `/api/uploads/:id/mappings`        | Create scenario mapping                                              |
| PUT    | `/api/mappings/:id`                | Update mapping                                                       |
| DELETE | `/api/mappings/:id`                | Delete mapping                                                       |
| GET    | `/api/uploads/:id/mapping-options` | Distinct ProductType/CCY/Segment/Transactional for dropdowns         |
| POST   | `/api/uploads/:id/reprocess`       | Reprocess upload after behaviour/mapping changes                     |
| GET    | `/api/presets`                     | List user's presets                                                  |
| POST   | `/api/presets`                     | Create preset                                                        |
| PUT    | `/api/presets/:id`                 | Update preset                                                        |
| DELETE | `/api/presets/:id`                 | Delete preset                                                        |
| GET    | `/api/reference-maps`              | Get all reference tables (all authenticated users)                   |

### SuperAdmin Only

| Method | Path                        | Description                |
| ------ | --------------------------- | -------------------------- |
| GET    | `/api/reference/:table`     | List reference table items |
| POST   | `/api/reference/:table`     | Add reference item         |
| PUT    | `/api/reference/:table/:id` | Update reference item      |
| DELETE | `/api/reference/:table/:id` | Delete reference item      |

### `filter_type` Parameter

Used by Results, Summary, Pivot, Export endpoints:

- `"bbi"` — principal only
- `"interest"` — interest only
- `"both"` — sum principal + interest into the principal fields

### `behaviour_id` Parameter

Used by Results, Summary, Export:

- Empty / `"null"` / `"base"` → base results (`behaviour_id IS NULL`)
- A numeric ID → scenario results for that specific behaviour

---

## Scenario File Format

### CSV Format (2-Section)

Sections are separated by a blank line. Delimiter auto-detected (comma or semicolon).

**Section 1 — Bucket Configuration:**

```
Bucket Type,Bucket Name,Percentage,ProductType,CCY,Segment,Transactional/Non Transactional,Value Type
LCR,CF <= 30D,100%,Loan,IDR,Retail,Transactional,Outstanding
```

**Section 2 — Cashflow Assumption:**

```
ProductType,CCY,Segment,Transactional/Non Transactional,Bucket Type,Cashflow Assumption
Loan,IDR,Retail,Transactional,LCR,90%
```

### XLSX Format (2-Sheet)

- **Sheet "Bucket"** — same columns as CSV Section 1
- **Sheet "Cashflow Assumption"** — same columns as CSV Section 2
- Percentage values > 1.0 are automatically divided by 100
- Parsed by `parseScenarioXLSX()` in `handlers/behaviour.go`

**Validation:** Overlap detection across both sections — if two rules match the same loan criteria for the same bucket type, it's rejected. `"All"` acts as wildcard (empty string does NOT). Additionally, exact duplicate rows (same BucketType, BucketName, ProductType, CCY, Segment, Transactional) are rejected.

---

## Deployment

Docker Compose runs 3 services:

1. **db** — postgres:16-alpine with healthcheck
2. **api** — Go binary (CGO_ENABLED=0, Alpine)
3. **nginx** — SSL termination with self-signed cert, proxies to api:8002

Production URL: `https://103.103.22.207:8002`

---

## Common Pitfalls

- **Reprocess must NOT re-insert loan_inputs** — it reads existing rows from DB (preserving their IDs) and only regenerates `cashflow_results`
- **Reprocess race condition fix** — `DELETE FROM cashflow_results` runs inside the goroutine, not before spawning it. Setting status to `'processing'` first prevents concurrent calls.
- **EndDate is nullable** — `nil` EndDate means a no-maturity loan (uses default behaviour). Always check `loan.HasEndDate()` before generating schedule.
- **Interest Rate handling** — CSV: divided by 100 in validator (9% → 0.09). XLSX: kept as-is (already decimal, 0.09 = 9%)
- **CORS is `*`** — currently allows all origins
- **Bucket JSONB columns** — stored as `map[string]float64` serialised to JSON. Keys are bucket labels like `"≤ 1 M"`, `"CF <= 30D"`, etc.
- **result_type naming** — `"Contractual"` for amortized loans, `"Default Behaviour"` for no-maturity loans, scenario name (e.g., `"Covid Behaviour"`) for scenarios
- **Default behaviour cannot be modified or deleted** — protected in `UpdateBehaviour` and `DeleteBehaviour` handlers
- **Pivot aggregation** — bucket sums are computed in Go, not SQL. For each group, a second query fetches all JSONB rows and sums them in-memory.
- **Migration files run on every startup** — uses `CREATE TABLE IF NOT EXISTS` and `ALTER TABLE ... ADD COLUMN IF NOT EXISTS` for idempotency
- **method / day_count mapping** — validator accepts both string names and numeric IDs (1=Annuity/30/360, 2=Flat/ACT/360, 3=ACT/365)
- **Indonesian number format** — validator handles `1.000.000,50` → `1000000.50`
- **Scenario overlap validator** — mirrors `installment_software/validator.py`, checks within Bucket section, within Cashflow section, and cross-section. Also detects exact duplicate rows (Step 0). Only `"All"` acts as wildcard — empty string does NOT (fixed to match Python)
- **Asset/Liability sign flipping** — when `loan.AssetLiability == 2`, all bucket values (principal + interest) are negated via `negateBucketMap()` after calculation, before insert
- **ILAAP bucketing** — 41 granular buckets with day-level precision. `ilaapMonthsBetween()` uses relativedelta semantics where residual days increment month count
- **XLSX data files** — use `validator/xlsx.go` via excelize. Excel date serial numbers (e.g., `44927`) are converted to `time.Time`. Float values for Method/DayCount/InstrumentType (e.g., `"1.0"`) are truncated to integers before ID mapping
- **XLSX scenario files** — `parseScenarioXLSX()` reads "Bucket" and "Cashflow Assumption" sheets. Percentage values > 1.0 are divided by 100
