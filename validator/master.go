package validator

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"bs-be/config"
	"bs-be/models"
)

// ─────────────────────────────────────────────────────────────
//  MASTER DATA TABLES
// ─────────────────────────────────────────────────────────────

// MasterTable describes one reference ("master data") table.
type MasterTable struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

// MasterTables is the ordered catalog of every reference table. It is the
// single source of truth used by the reference CRUD handlers, the master data
// guide endpoint and the Excel template generator.
var MasterTables = []MasterTable{
	{"product_types", "Product Type", "Kind of product the account belongs to"},
	{"segments", "Segment", "Customer segment"},
	{"methods", "Method", "Amortization method used to build the schedule"},
	{"day_counts", "Day Count", "Day count convention used for interest accrual"},
	{"currencies", "Currency (CCY)", "Currency of the account"},
	{"instrument_types", "Instrument Type", "Rate behaviour of the instrument"},
	{"transactional_types", "Transactional Type", "Whether the account is transactional"},
	{"installment_frequencies", "Installment Frequency", "Number of months between payments"},
	{"insured_types", "Insured / Uninsured", "Whether the account is covered by insurance"},
	{"asset_liabilities", "Asset / Liability", "Side of the balance sheet — liabilities are reported with a negative sign"},
	{"revolving_flags", "Revolving Flag", "Whether the facility is revolving"},
}

// MasterTableLabel returns the human label for a table key.
func MasterTableLabel(key string) string {
	for _, t := range MasterTables {
		if t.Key == key {
			return t.Label
		}
	}
	return key
}

// IsMasterTable reports whether key names a known reference table.
func IsMasterTable(key string) bool {
	for _, t := range MasterTables {
		if t.Key == key {
			return true
		}
	}
	return false
}

// ─────────────────────────────────────────────────────────────
//  MASTER DATA SNAPSHOT
// ─────────────────────────────────────────────────────────────

// MasterData is an immutable snapshot of every reference table, with lookups
// prepared for validation.
type MasterData struct {
	Items  map[string][]models.ReferenceItem
	byID   map[string]map[string]string
	byName map[string]map[string]string
}

// Has reports whether id exists in the given table.
func (m *MasterData) Has(table, id string) bool {
	ids, ok := m.byID[table]
	if !ok {
		return false
	}
	_, found := ids[id]
	return found
}

// IsEmpty reports whether a table has no rows at all. An empty table means the
// master data was never seeded (or was wiped by a superadmin); validation is
// skipped for such columns so a misconfigured table cannot lock out uploads.
func (m *MasterData) IsEmpty(table string) bool {
	return len(m.byID[table]) == 0
}

// Name returns the display name for a stored value. It accepts an ID ("1") or
// an already-resolved name in any casing ("annuity"), and falls back to the
// value itself when the table knows nothing about it.
func (m *MasterData) Name(table, value string) string {
	if value == "" {
		return ""
	}
	if n, ok := m.byID[table][value]; ok {
		return n
	}
	if id, ok := m.byName[table][strings.ToLower(value)]; ok {
		return m.byID[table][id]
	}
	return value
}

// ResolveID accepts either the ID ("1") or the display name ("Annuity", case
// insensitive) and returns the canonical ID.
func (m *MasterData) ResolveID(table, raw string) (string, bool) {
	raw = normalizeCode(raw)
	if raw == "" {
		return "", false
	}
	if m.Has(table, raw) {
		return raw, true
	}
	if id, ok := m.byName[table][strings.ToLower(raw)]; ok {
		return id, true
	}
	return "", false
}

// Allowed renders the permitted values of a table as "1 = Loan, 2 = Deposit".
func (m *MasterData) Allowed(table string) string {
	items := m.Items[table]
	parts := make([]string, 0, len(items))
	for _, it := range items {
		parts = append(parts, fmt.Sprintf("%s = %s", it.ID, it.Name))
	}
	return strings.Join(parts, ", ")
}

// normalizeCode trims a raw cell and collapses Excel's float rendering
// ("1.0" → "1") so numeric IDs compare cleanly. Non-numeric codes (e.g. "IDR")
// pass through untouched.
func normalizeCode(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil && f == float64(int64(f)) {
		return strconv.FormatInt(int64(f), 10)
	}
	return s
}

// isBlankCode reports whether a raw cell should be treated as "not supplied".
func isBlankCode(raw string) bool {
	s := strings.ToUpper(strings.TrimSpace(raw))
	return s == "" || s == "NULL" || s == "NA" || s == "N/A" || s == "NONE" || s == "-"
}

// ─────────────────────────────────────────────────────────────
//  LOADING & CACHING
// ─────────────────────────────────────────────────────────────

var (
	masterMu     sync.RWMutex
	masterCache  *MasterData
	masterLoaded time.Time
)

const masterTTL = 30 * time.Second

// InvalidateMasterData drops the cached snapshot. Called after any reference
// table write so the next upload validates against the new master data.
func InvalidateMasterData() {
	masterMu.Lock()
	masterCache = nil
	masterMu.Unlock()
}

// LoadMasterData returns a cached snapshot of all reference tables.
func LoadMasterData() *MasterData {
	masterMu.RLock()
	if masterCache != nil && time.Since(masterLoaded) < masterTTL {
		md := masterCache
		masterMu.RUnlock()
		return md
	}
	masterMu.RUnlock()

	md := fetchMasterData()

	masterMu.Lock()
	masterCache = md
	masterLoaded = time.Now()
	masterMu.Unlock()

	return md
}

func fetchMasterData() *MasterData {
	md := &MasterData{
		Items:  make(map[string][]models.ReferenceItem, len(MasterTables)),
		byID:   make(map[string]map[string]string, len(MasterTables)),
		byName: make(map[string]map[string]string, len(MasterTables)),
	}

	for _, t := range MasterTables {
		items := make([]models.ReferenceItem, 0)
		ids := make(map[string]string)
		names := make(map[string]string)

		if config.DB != nil {
			rows, err := config.DB.Query(
				"SELECT id, name FROM " + t.Key + " ORDER BY LENGTH(id), id",
			)
			if err != nil {
				log.Printf("master data: failed to read %s: %v", t.Key, err)
			} else {
				for rows.Next() {
					var it models.ReferenceItem
					if rows.Scan(&it.ID, &it.Name) == nil {
						items = append(items, it)
						ids[it.ID] = it.Name
						names[strings.ToLower(it.Name)] = it.ID
					}
				}
				rows.Close()
			}
		}

		md.Items[t.Key] = items
		md.byID[t.Key] = ids
		md.byName[t.Key] = names
	}

	return md
}

// ─────────────────────────────────────────────────────────────
//  CODE VALIDATION
// ─────────────────────────────────────────────────────────────

// checkCode validates one coded cell against its master data table.
//
// It returns the canonical ID to store. When the cell is blank it returns "".
// When the value is not in the master data it returns a ValidationError whose
// message spells out every accepted code, so the person building the
// spreadsheet can fix it without guessing.
func (m *MasterData) checkCode(
	table, column, raw string, rowNum int, required bool,
) (string, *models.ValidationError) {
	label := MasterTableLabel(table)

	if isBlankCode(raw) {
		if required {
			return "", &models.ValidationError{
				Row: rowNum, Column: column,
				Message: fmt.Sprintf(
					"%s is required but the cell is empty. Allowed values: %s.",
					column, m.Allowed(table),
				),
			}
		}
		return "", nil
	}

	// A wiped master table must not block every upload.
	if m.IsEmpty(table) {
		return normalizeCode(raw), nil
	}

	id, ok := m.ResolveID(table, raw)
	if !ok {
		return "", &models.ValidationError{
			Row: rowNum, Column: column,
			Message: fmt.Sprintf(
				"'%s' is not in the %s master data. Allowed values: %s. "+
					"Ask a SuperAdmin to add it under Admin → Master Data if the code is new.",
				strings.TrimSpace(raw), label, m.Allowed(table),
			),
		}
	}
	return id, nil
}
