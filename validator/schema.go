package validator

import "strings"

// ─────────────────────────────────────────────────────────────
//  INPUT FILE SCHEMA
// ─────────────────────────────────────────────────────────────

// ColumnSpec describes one column of the loan input file. This catalog is the
// single source of truth for:
//   - which headers are required (RequiredColumns)
//   - which header spellings are accepted (columnAliases)
//   - which master data table a coded column is validated against
//   - the Master Data guide shown in the admin panel
//   - the downloadable Excel template
type ColumnSpec struct {
	Name        string   `json:"name"`     // canonical header as written in the file
	Required    bool     `json:"required"` // header must be present
	Kind        string   `json:"kind"`     // code | date | number | percent | text | boolean
	RefTable    string   `json:"ref_table"`
	Format      string   `json:"format"`
	Example     string   `json:"example"`
	Description string   `json:"description"`
	Nullable    bool     `json:"nullable"` // cell may be left blank
	Aliases     []string `json:"aliases"`
}

// InputColumns lists every column the uploader understands, in template order.
var InputColumns = []ColumnSpec{
	{
		Name: "Reporting Date", Required: true, Kind: "date",
		Format: "DD/MM/YYYY", Example: "31/12/2025",
		Description: "Position date the row is reported as of.",
		Aliases:     []string{"reporting date", "reportingdate", "reporting_date"},
	},
	{
		Name: "Account ID", Required: true, Kind: "text",
		Format: "Free text, must be unique per row", Example: "ACC-00001",
		Description: "Identifier of the account. Cannot be empty.",
		Aliases:     []string{"accountid", "account_id", "account id"},
	},
	{
		Name: "Account Number", Kind: "text", Nullable: true,
		Format: "Free text", Example: "1234567890",
		Description: "Optional bank account number, carried through to the output.",
		Aliases:     []string{"account number", "accountnumber", "account_number"},
	},
	{
		Name: "CCY", Required: true, Kind: "code", RefTable: "currencies",
		Format: "Currency code from master data", Example: "IDR",
		Description: "Currency of the account.",
		Aliases:     []string{"ccy", "currency"},
	},
	{
		Name: "Outstanding", Required: true, Kind: "number",
		Format: "Number, no thousand separators needed", Example: "1000000",
		Description: "Outstanding balance. Must be zero or positive — use Asset_Liability = 2 to report a liability, not a negative number.",
		Aliases:     []string{"outstanding"},
	},
	{
		Name: "Interest Rate", Required: true, Kind: "percent",
		Format: "XLSX: decimal (0.09 = 9%) · CSV: percent (9 = 9%)", Example: "0.09",
		Description: "Annual interest rate. Excel files store it as a decimal, CSV files as a percentage.",
		Aliases:     []string{"interest rate", "interestrate", "interest_rate"},
	},
	{
		Name: "Margin", Kind: "number", Nullable: true,
		Format: "Decimal (0.02 = 2%)", Example: "0.02",
		Description: "Optional margin on top of the interest rate.",
		Aliases:     []string{"margin"},
	},
	{
		Name: "Start Date", Kind: "date", Nullable: true,
		Format: "DD/MM/YYYY", Example: "01/01/2024",
		Description: "Disbursement / opening date. Must not be after End Date.",
		Aliases:     []string{"start date", "startdate", "start_date"},
	},
	{
		Name: "End Date", Required: true, Kind: "date", Nullable: true,
		Format: "DD/MM/YYYY, or leave blank / NULL", Example: "31/12/2027",
		Description: "Maturity date. Leave blank for non-maturing accounts — those are bucketed using the default behaviour instead of an amortization schedule.",
		Aliases:     []string{"end date", "enddate", "end_date"},
	},
	{
		Name: "Installment Frequency", Required: true, Kind: "code",
		RefTable: "installment_frequencies", Nullable: true,
		Format: "Code from master data, or blank / NULL for bullet", Example: "1",
		Description: "Months between principal installments. Blank or NULL means a bullet repayment at maturity.",
		Aliases:     []string{"installment", "installment frequency", "installmentfrequency", "installment_frequency"},
	},
	{
		Name: "Interest Payment Frequency", Required: true, Kind: "code",
		RefTable: "installment_frequencies", Nullable: true,
		Format: "Code from master data, or blank / NULL", Example: "1",
		Description: "Months between interest payments. Blank or NULL means interest is settled with the principal.",
		Aliases:     []string{"interest payment frequency", "interestpaymentfrequency", "interest_payment_frequency"},
	},
	{
		Name: "Method", Required: true, Kind: "code", RefTable: "methods",
		Format: "Code from master data", Example: "1",
		Description: "Amortization method used to build the payment schedule.",
		Aliases:     []string{"method"},
	},
	{
		Name: "Day Count", Required: true, Kind: "code", RefTable: "day_counts",
		Format: "Code from master data", Example: "1",
		Description: "Day count convention used for interest accrual.",
		Aliases:     []string{"day count", "daycount", "day_count"},
	},
	{
		Name: "ProductType", Kind: "code", RefTable: "product_types", Nullable: true,
		Format: "Code from master data", Example: "1",
		Description: "Product the account belongs to.",
		Aliases:     []string{"product type", "producttype", "product_type"},
	},
	{
		Name: "Segment", Kind: "code", RefTable: "segments", Nullable: true,
		Format: "Code from master data", Example: "1",
		Description: "Customer segment.",
		Aliases:     []string{"segment"},
	},
	{
		Name: "Instrument Type", Kind: "code", RefTable: "instrument_types", Nullable: true,
		Format: "Code from master data", Example: "1",
		Description: "Rate behaviour of the instrument.",
		Aliases:     []string{"instrument type", "instrumenttype", "instrument_type"},
	},
	{
		Name: "Transactional/Non Transactional", Kind: "code",
		RefTable: "transactional_types", Nullable: true,
		Format: "Code from master data", Example: "1",
		Description: "Whether the account is used transactionally.",
		Aliases: []string{
			"transactional/non transactional", "transactional non transactional",
			"transactionalnontransactional", "transactional_or_non", "transactional",
		},
	},
	{
		Name: "Insured/Uninsured", Kind: "code", RefTable: "insured_types", Nullable: true,
		Format: "Code from master data", Example: "1",
		Description: "Whether the account is covered by deposit insurance.",
		Aliases: []string{
			"insured/uninsured", "insured uninsured", "insureduninsured",
			"insured_or_uninsured", "insured",
		},
	},
	{
		Name: "Asset_Liability", Kind: "code", RefTable: "asset_liabilities", Nullable: true,
		Format: "Code from master data (defaults to Asset)", Example: "1",
		Description: "Balance sheet side. Liability rows have every bucket value negated.",
		Aliases:     []string{"asset_liability", "asset liability", "assetliability"},
	},
	{
		Name: "Revolving_flag", Kind: "code", RefTable: "revolving_flags", Nullable: true,
		Format: "Code from master data", Example: "2",
		Description: "Whether the facility is revolving.",
		Aliases:     []string{"revolving_flag", "revolving flag", "revolvingflag"},
	},
	{
		Name: "Market Value", Kind: "number", Nullable: true,
		Format: "Number", Example: "1050000",
		Description: "Market value of the instrument. Used by scenarios whose Value Type is 'Market Value'.",
		Aliases:     []string{"market value", "marketvalue", "market_value"},
	},
	{
		Name: "Daerah", Kind: "text", Nullable: true,
		Format: "Free text", Example: "Jakarta",
		Description: "Region label, used for grouping and pivoting only.",
		Aliases:     []string{"daerah", "region"},
	},
	{
		Name: "KodePos", Kind: "text", Nullable: true,
		Format: "Free text", Example: "12190",
		Description: "Postal code, used for grouping and pivoting only.",
		Aliases:     []string{"kodepos", "kode pos", "kode_pos"},
	},
	{
		Name: "Default Behaviour", Kind: "boolean", Nullable: true,
		Format: "TRUE / FALSE (blank means TRUE)", Example: "TRUE",
		Description: "Set FALSE to exclude the row from default-behaviour bucketing.",
		Aliases:     []string{"default behaviour", "defaultbehaviour", "default_behaviour"},
	},
}

// SupportedMethods are the amortization methods the calculator can actually
// run. A master data row whose name is outside this set is rejected at upload
// with an explicit message rather than silently producing a wrong schedule.
var SupportedMethods = []string{"annuity", "flat"}

// SupportedDayCounts are the conventions yearfrac.go implements.
var SupportedDayCounts = []string{"30/360", "ACT/360", "ACT/365"}

// ColumnSpecFor returns the spec for a canonical column name.
func ColumnSpecFor(name string) (ColumnSpec, bool) {
	for _, c := range InputColumns {
		if c.Name == name {
			return c, true
		}
	}
	return ColumnSpec{}, false
}

// RequiredColumns / OptionalColumns are derived from InputColumns so the
// catalog stays the only place a column is declared.
var RequiredColumns = buildRequiredColumns()
var OptionalColumns = buildOptionalColumns()

// columnAliases maps every accepted header spelling (lowercased) to its
// canonical name.
var columnAliases = buildColumnAliases()

func buildRequiredColumns() []string {
	var out []string
	for _, c := range InputColumns {
		if c.Required {
			out = append(out, c.Name)
		}
	}
	return out
}

func buildOptionalColumns() []string {
	var out []string
	for _, c := range InputColumns {
		if !c.Required {
			out = append(out, c.Name)
		}
	}
	return out
}

func buildColumnAliases() map[string]string {
	m := make(map[string]string)
	for _, c := range InputColumns {
		m[strings.ToLower(c.Name)] = c.Name
		for _, a := range c.Aliases {
			m[strings.ToLower(a)] = c.Name
		}
	}
	return m
}
