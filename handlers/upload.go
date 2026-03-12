package handlers

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"bs-be/calculator"
	"bs-be/config"
	"bs-be/models"
	"bs-be/validator"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// loanWithID pairs a loan_input database ID with its parsed Loan data (used for reprocessing)
type loanWithID struct {
	InputID int64
	Loan    models.Loan
}

func UploadCSV(c *gin.Context) {
	userID, _ := c.Get("user_id")

	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No file uploaded"})
		return
	}
	defer file.Close()

	// Read file content
	content, err := io.ReadAll(file)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to read file"})
		return
	}

	// Validate CSV
	loans, validationErrors := validator.ValidateAndParseCSV(bytes.NewReader(content), 50)
	if len(validationErrors) > 0 {
		c.JSON(http.StatusBadRequest, models.UploadResponse{
			Status: "failed",
			Errors: validationErrors,
		})
		return
	}

	// Save file to disk
	uploadDir := config.GetUploadDir()
	os.MkdirAll(uploadDir, 0755)

	uploadID := uuid.New().String()
	filename := header.Filename
	savePath := filepath.Join(uploadDir, fmt.Sprintf("%s_%s", uploadID, filename))

	if err := os.WriteFile(savePath, content, 0644); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save file"})
		return
	}

	// Insert upload record
	_, err = config.DB.Exec(
		`INSERT INTO uploads (id, user_id, filename, total_rows, status, file_path)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		uploadID, userID, filename, len(loans), "processing", savePath,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create upload record"})
		return
	}

	// Process in background
	go processLoans(uploadID, loans)

	c.JSON(http.StatusOK, models.UploadResponse{
		ID:       uploadID,
		Filename: filename,
		Status:   "processing",
	})
}

// scenarioInfo holds a loaded scenario for batch processing
type scenarioInfo struct {
	BehaviourID   int64
	BehaviourName string
	Data          *calculator.ScenarioData
}

func processLoans(uploadID string, loans []models.Loan) {
	batchSize := 500

	// Load default behaviour weights once
	defaultBehaviourID := calculator.LoadDefaultBehaviourID()
	var defaultWeights calculator.BehaviourWeights
	if defaultBehaviourID > 0 {
		defaultWeights = calculator.LoadBehaviourWeights(defaultBehaviourID)
	}

	// Load scenarios (custom behaviours that are scenarios)
	scenarios := loadScenarios(uploadID)

	for i := 0; i < len(loans); i += batchSize {
		end := i + batchSize
		if end > len(loans) {
			end = len(loans)
		}
		batch := loans[i:end]

		err := processBatch(uploadID, batch, i, defaultWeights, scenarios)
		if err != nil {
			log.Printf("Error processing batch starting at row %d: %v", i, err)
			config.DB.Exec(
				"UPDATE uploads SET status = 'failed', error_message = $1 WHERE id = $2",
				fmt.Sprintf("Processing error at row %d: %v", i+1, err), uploadID,
			)
			return
		}
	}

	// Mark as completed
	config.DB.Exec("UPDATE uploads SET status = 'completed' WHERE id = $1", uploadID)
	log.Printf("Upload %s processed successfully (%d loans)", uploadID, len(loans))
}

// loadScenarios loads all scenario behaviours for an upload
func loadScenarios(uploadID string) []scenarioInfo {
	var scenarios []scenarioInfo

	rows, err := config.DB.Query(
		`SELECT id, name FROM behaviours
		 WHERE upload_id = $1 AND is_default = FALSE AND is_scenario = TRUE
		 ORDER BY id`,
		uploadID,
	)
	if err != nil {
		log.Printf("Failed to load scenarios for upload %s: %v", uploadID, err)
		return scenarios
	}
	defer rows.Close()

	for rows.Next() {
		var si scenarioInfo
		if rows.Scan(&si.BehaviourID, &si.BehaviourName) == nil {
			si.Data = calculator.LoadScenarioData(si.BehaviourID)
			scenarios = append(scenarios, si)
		}
	}

	return scenarios
}

// insertResult is a helper that inserts a cashflow_results row
func insertResult(tx *sql.Tx, uploadID string, loanInputID int64, remainingDays int,
	irrbbP, irrbbI, lcrP, lcrI, nsfrP, nsfrI map[string]float64, resultType string, behaviourID *int64) error {

	irrbbPJSON, _ := json.Marshal(irrbbP)
	irrbbIJSON, _ := json.Marshal(irrbbI)
	lcrPJSON, _ := json.Marshal(lcrP)
	lcrIJSON, _ := json.Marshal(lcrI)
	nsfrPJSON, _ := json.Marshal(nsfrP)
	nsfrIJSON, _ := json.Marshal(nsfrI)

	_, err := tx.Exec(
		`INSERT INTO cashflow_results (
			upload_id, loan_input_id, remaining_days,
			irrbb_principal, irrbb_interest,
			lcr_principal, lcr_interest,
			nsfr_principal, nsfr_interest,
			result_type, behaviour_id
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		uploadID, loanInputID, remainingDays,
		irrbbPJSON, irrbbIJSON, lcrPJSON, lcrIJSON, nsfrPJSON, nsfrIJSON,
		resultType, behaviourID,
	)
	return err
}

func processBatch(uploadID string, loans []models.Loan, startIdx int, defaultWeights calculator.BehaviourWeights, scenarios []scenarioInfo) error {
	tx, err := config.DB.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	for j, loan := range loans {
		rowNum := startIdx + j + 1

		// Handle nullable end_date for SQL
		var endDateSQL interface{}
		if loan.EndDate != nil {
			endDateSQL = *loan.EndDate
		} else {
			endDateSQL = nil
		}

		// Insert loan input
		var loanInputID int64
		err := tx.QueryRow(
			`INSERT INTO loan_inputs (
				upload_id, row_number, reporting_date, account_id, ccy, outstanding,
				interest_rate, start_date, end_date, installment_frequency,
				product_type, segment, daerah, kode_pos, insured_or_uninsured,
				transactional_or_non, method, interest_payment_frequency, day_count,
				default_behaviour, instrument_type, market_value
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22)
			RETURNING id`,
			uploadID, rowNum,
			loan.ReportingDate, loan.AccountID, loan.CCY, loan.Outstanding,
			loan.InterestRate, loan.StartDate, endDateSQL, loan.InstallmentFrequency,
			loan.ProductType, loan.Segment, loan.Daerah, loan.KodePos,
			loan.InsuredOrUninsured, loan.TransactionalOrNon, loan.Method,
			loan.InterestPaymentFrequency, loan.DayCount,
			loan.DefaultBehaviour, loan.InstrumentType, loan.MarketValue,
		).Scan(&loanInputID)
		if err != nil {
			return fmt.Errorf("failed to insert loan input row %d: %w", rowNum, err)
		}

		// ===== BASE RESULT (Amortization OR Default Behaviour) =====
		// If has end_date -> use Amortization schedule
		// If no end_date -> use Default Behaviour weights
		var irrbbP, irrbbI, lcrP, lcrI, nsfrP, nsfrI map[string]float64
		var remainingDays int

		if loan.HasEndDate() {
			// Normal calculation: generate amortization schedule
			schedule := calculator.GenerateSchedule(&loan)
			irrbbP, irrbbI, lcrP, lcrI, nsfrP, nsfrI = calculator.ComputeAllBuckets(schedule, loan.ReportingDate)
			remainingDays = loan.TenorDays()
		} else {
			// Default behaviour: use weight-based distribution
			if defaultWeights != nil {
				irrbbP, irrbbI, lcrP, lcrI, nsfrP, nsfrI = calculator.ComputeBehaviourBuckets(loan.Outstanding, defaultWeights)
			} else {
				irrbbP = calculator.EmptyBucketMap(calculator.IRRBBLabels)
				irrbbI = calculator.EmptyBucketMap(calculator.IRRBBLabels)
				lcrP = calculator.EmptyBucketMap(calculator.LCRLabels)
				lcrI = calculator.EmptyBucketMap(calculator.LCRLabels)
				nsfrP = calculator.EmptyBucketMap(calculator.NSFRLabels)
				nsfrI = calculator.EmptyBucketMap(calculator.NSFRLabels)
			}
			remainingDays = 0
		}

		if err := insertResult(tx, uploadID, loanInputID, remainingDays,
			irrbbP, irrbbI, lcrP, lcrI, nsfrP, nsfrI, "Base", nil); err != nil {
			return fmt.Errorf("failed to insert base result row %d: %w", rowNum, err)
		}

		// ===== SCENARIO RESULTS =====
		for _, sc := range scenarios {
			scIrrbbP, scIrrbbI, scLcrP, scLcrI, scNsfrP, scNsfrI := calculator.ComputeAllScenarioBuckets(
				sc.Data,
				loan.Outstanding,
				loan.MarketValue,
				loan.ProductType, loan.CCY, loan.Segment, loan.TransactionalOrNon,
				irrbbP, irrbbI, lcrP, lcrI, nsfrP, nsfrI, // fallback to base result
			)

			bid := sc.BehaviourID
			if err := insertResult(tx, uploadID, loanInputID, remainingDays,
				scIrrbbP, scIrrbbI, scLcrP, scLcrI, scNsfrP, scNsfrI, "Scenario", &bid); err != nil {
				return fmt.Errorf("failed to insert scenario '%s' result row %d: %w", sc.BehaviourName, rowNum, err)
			}
		}
	}

	return tx.Commit()
}

// ReprocessUpload re-runs the calculation after behaviours/mappings have been added
func ReprocessUpload(c *gin.Context) {
	uploadID := c.Param("id")
	userID, _ := c.Get("user_id")

	// Verify ownership + check not already processing
	var status string
	err := config.DB.QueryRow(
		"SELECT status FROM uploads WHERE id = $1 AND user_id = $2",
		uploadID, userID,
	).Scan(&status)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Upload not found"})
		return
	}
	if status == "processing" {
		c.JSON(http.StatusConflict, gin.H{"error": "Already processing"})
		return
	}

	// Mark as processing first (prevents concurrent calls)
	config.DB.Exec("UPDATE uploads SET status = 'processing' WHERE id = $1", uploadID)

	// Re-read existing loan_inputs WITH their IDs (DO NOT re-insert them)
	rows, err := config.DB.Query(
		`SELECT id, reporting_date, account_id, ccy, outstanding, interest_rate,
		        start_date, end_date, installment_frequency,
		        product_type, segment, daerah, kode_pos,
		        insured_or_uninsured, transactional_or_non, method,
		        interest_payment_frequency, day_count,
		        COALESCE(default_behaviour, TRUE), COALESCE(instrument_type, ''),
				COALESCE(market_value, 0)
		 FROM loan_inputs WHERE upload_id = $1 ORDER BY row_number`,
		uploadID,
	)
	if err != nil {
		config.DB.Exec("UPDATE uploads SET status = 'completed' WHERE id = $1", uploadID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read loans"})
		return
	}
	defer rows.Close()

	var loanRows []loanWithID
	for rows.Next() {
		var lw loanWithID
		var endDate sql.NullTime
		var installmentFreq, interestPaymentFreq sql.NullInt64

		err := rows.Scan(
			&lw.InputID,
			&lw.Loan.ReportingDate, &lw.Loan.AccountID, &lw.Loan.CCY, &lw.Loan.Outstanding,
			&lw.Loan.InterestRate, &lw.Loan.StartDate, &endDate, &installmentFreq,
			&lw.Loan.ProductType, &lw.Loan.Segment, &lw.Loan.Daerah, &lw.Loan.KodePos,
			&lw.Loan.InsuredOrUninsured, &lw.Loan.TransactionalOrNon, &lw.Loan.Method,
			&interestPaymentFreq, &lw.Loan.DayCount,
			&lw.Loan.DefaultBehaviour, &lw.Loan.InstrumentType, &lw.Loan.MarketValue,
		)
		if err != nil {
			log.Printf("Error reading loan: %v", err)
			continue
		}

		if endDate.Valid {
			lw.Loan.EndDate = &endDate.Time
		}
		if installmentFreq.Valid {
			v := int(installmentFreq.Int64)
			lw.Loan.InstallmentFrequency = &v
		}
		if interestPaymentFreq.Valid {
			v := int(interestPaymentFreq.Int64)
			lw.Loan.InterestPaymentFrequency = &v
		}

		loanRows = append(loanRows, lw)
	}

	// Process in background: delete old results + regenerate (all inside goroutine)
	go reprocessFromDB(uploadID, loanRows)

	c.JSON(http.StatusOK, gin.H{"message": "Reprocessing started", "total_rows": len(loanRows)})
}

// reprocessFromDB deletes old cashflow_results and regenerates from existing loan_inputs
func reprocessFromDB(uploadID string, loanRows []loanWithID) {
	// Delete existing results first (inside the goroutine to prevent race condition)
	_, err := config.DB.Exec("DELETE FROM cashflow_results WHERE upload_id = $1", uploadID)
	if err != nil {
		log.Printf("Error deleting old results for %s: %v", uploadID, err)
		config.DB.Exec("UPDATE uploads SET status = 'failed', error_message = $1 WHERE id = $2",
			"Failed to clear old results", uploadID)
		return
	}

	// Load default behaviour weights
	defaultBehaviourID := calculator.LoadDefaultBehaviourID()
	var defaultWeights calculator.BehaviourWeights
	if defaultBehaviourID > 0 {
		defaultWeights = calculator.LoadBehaviourWeights(defaultBehaviourID)
	}

	// Load scenarios
	scenarios := loadScenarios(uploadID)

	batchSize := 500
	for i := 0; i < len(loanRows); i += batchSize {
		end := i + batchSize
		if end > len(loanRows) {
			end = len(loanRows)
		}
		batch := loanRows[i:end]

		err := reprocessBatch(uploadID, batch, defaultWeights, scenarios)
		if err != nil {
			log.Printf("Error reprocessing batch starting at %d: %v", i, err)
			config.DB.Exec(
				"UPDATE uploads SET status = 'failed', error_message = $1 WHERE id = $2",
				fmt.Sprintf("Reprocessing error at row %d: %v", i+1, err), uploadID,
			)
			return
		}
	}

	config.DB.Exec("UPDATE uploads SET status = 'completed' WHERE id = $1", uploadID)
	log.Printf("Upload %s reprocessed successfully (%d loans)", uploadID, len(loanRows))
}

// reprocessBatch generates cashflow_results for existing loan_inputs (does NOT re-insert loan_inputs)
func reprocessBatch(uploadID string, loanRows []loanWithID, defaultWeights calculator.BehaviourWeights, scenarios []scenarioInfo) error {
	tx, err := config.DB.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	for _, lw := range loanRows {
		loanInputID := lw.InputID
		loan := lw.Loan

		// ===== SINGLE RESULT: "Behaviour" =====
		var irrbbP, irrbbI, lcrP, lcrI, nsfrP, nsfrI map[string]float64
		var remainingDays int

		if loan.HasEndDate() {
			schedule := calculator.GenerateSchedule(&loan)
			irrbbP, irrbbI, lcrP, lcrI, nsfrP, nsfrI = calculator.ComputeAllBuckets(schedule, loan.ReportingDate)
			remainingDays = loan.TenorDays()
		} else {
			if defaultWeights != nil {
				irrbbP, irrbbI, lcrP, lcrI, nsfrP, nsfrI = calculator.ComputeBehaviourBuckets(loan.Outstanding, defaultWeights)
			} else {
				irrbbP = calculator.EmptyBucketMap(calculator.IRRBBLabels)
				irrbbI = calculator.EmptyBucketMap(calculator.IRRBBLabels)
				lcrP = calculator.EmptyBucketMap(calculator.LCRLabels)
				lcrI = calculator.EmptyBucketMap(calculator.LCRLabels)
				nsfrP = calculator.EmptyBucketMap(calculator.NSFRLabels)
				nsfrI = calculator.EmptyBucketMap(calculator.NSFRLabels)
			}
			remainingDays = 0
		}

		if err := insertResult(tx, uploadID, loanInputID, remainingDays,
			irrbbP, irrbbI, lcrP, lcrI, nsfrP, nsfrI, "Base", nil); err != nil {
			return fmt.Errorf("failed to insert base result: %w", err)
		}

		// ===== SCENARIO RESULTS =====
		for _, sc := range scenarios {
			scIrrbbP, scIrrbbI, scLcrP, scLcrI, scNsfrP, scNsfrI := calculator.ComputeAllScenarioBuckets(
				sc.Data,
				loan.Outstanding,
				loan.MarketValue,
				loan.ProductType, loan.CCY, loan.Segment, loan.TransactionalOrNon,
				irrbbP, irrbbI, lcrP, lcrI, nsfrP, nsfrI,
			)

			bid := sc.BehaviourID
			if err := insertResult(tx, uploadID, loanInputID, remainingDays,
				scIrrbbP, scIrrbbI, scLcrP, scLcrI, scNsfrP, scNsfrI, "Scenario", &bid); err != nil {
				return fmt.Errorf("failed to insert scenario '%s' result: %w", sc.BehaviourName, err)
			}
		}
	}

	return tx.Commit()
}

func GetUploadStatus(c *gin.Context) {
	uploadID := c.Param("id")

	var status, errorMsg string
	var totalRows int
	err := config.DB.QueryRow(
		"SELECT status, COALESCE(error_message, ''), total_rows FROM uploads WHERE id = $1",
		uploadID,
	).Scan(&status, &errorMsg, &totalRows)

	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Upload not found"})
		return
	}

	resp := gin.H{
		"id":         uploadID,
		"status":     status,
		"total_rows": totalRows,
	}
	if errorMsg != "" {
		resp["error_message"] = errorMsg
	}

	c.JSON(http.StatusOK, resp)
}

func GetHistory(c *gin.Context) {
	userID, _ := c.Get("user_id")

	rows, err := config.DB.Query(
		`SELECT id, filename, uploaded_at, total_rows, status, COALESCE(error_message, '')
		 FROM uploads WHERE user_id = $1 ORDER BY uploaded_at DESC`,
		userID,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch history"})
		return
	}
	defer rows.Close()

	var uploads []gin.H
	for rows.Next() {
		var id, filename, status, errorMsg string
		var uploadedAt time.Time
		var totalRows int

		if err := rows.Scan(&id, &filename, &uploadedAt, &totalRows, &status, &errorMsg); err != nil {
			continue
		}

		item := gin.H{
			"id":          id,
			"filename":    filename,
			"uploaded_at": uploadedAt.Format(time.RFC3339),
			"total_rows":  totalRows,
			"status":      status,
		}
		if errorMsg != "" {
			item["error_message"] = errorMsg
		}
		uploads = append(uploads, item)
	}

	if uploads == nil {
		uploads = []gin.H{}
	}

	c.JSON(http.StatusOK, uploads)
}

func DeleteUpload(c *gin.Context) {
	uploadID := c.Param("id")
	userID, _ := c.Get("user_id")

	// Check ownership
	var filePath string
	err := config.DB.QueryRow(
		"SELECT file_path FROM uploads WHERE id = $1 AND user_id = $2",
		uploadID, userID,
	).Scan(&filePath)

	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Upload not found"})
		return
	}

	// Delete from DB (cascades to loan_inputs, cashflow_results, behaviours, scenario_mappings)
	config.DB.Exec("DELETE FROM uploads WHERE id = $1", uploadID)

	// Delete file
	os.Remove(filePath)

	c.JSON(http.StatusOK, gin.H{"message": "Upload deleted"})
}

func GetResults(c *gin.Context) {
	uploadID := c.Param("id")

	// Parse pagination
	page := queryInt(c, "page", 1)
	limit := queryInt(c, "limit", 20)
	if limit > 100 {
		limit = 100
	}
	offset := (page - 1) * limit

	filterType := c.DefaultQuery("filter_type", "both") // bbi, interest, both
	sortBy := c.DefaultQuery("sort_by", "row_number")
	sortDir := c.DefaultQuery("sort_dir", "asc")

	// Validate sort direction
	if sortDir != "asc" && sortDir != "desc" {
		sortDir = "asc"
	}

	// Validate sort column
	validSortCols := map[string]string{
		"row_number":    "li.row_number",
		"account_id":    "li.account_id",
		"outstanding":   "li.outstanding",
		"interest_rate": "li.interest_rate",
		"ccy":           "li.ccy",
		"segment":       "li.segment",
		"product_type":  "li.product_type",
		"daerah":        "li.daerah",
		"method":        "li.method",
		"result_type":   "cr.result_type",
	}
	sortCol, ok := validSortCols[sortBy]
	if !ok {
		sortCol = "li.row_number"
	}

	// Parse filters
	filtersJSON := c.DefaultQuery("filters", "{}")
	var filters map[string]string
	json.Unmarshal([]byte(filtersJSON), &filters)

	behaviourID := c.Query("behaviour_id")

	// Build WHERE clause
	whereClause := "li.upload_id = $1"
	args := []interface{}{uploadID}
	argIdx := 2

	if behaviourID != "" && behaviourID != "null" && behaviourID != "base" {
		whereClause += fmt.Sprintf(" AND cr.behaviour_id = $%d", argIdx)
		args = append(args, behaviourID)
		argIdx++
	} else {
		whereClause += " AND cr.behaviour_id IS NULL"
	}

	filterColMap := map[string]string{
		"ccy":                    "li.ccy",
		"segment":                "li.segment",
		"product_type":           "li.product_type",
		"daerah":                 "li.daerah",
		"method":                 "li.method",
		"insured_or_uninsured":   "li.insured_or_uninsured",
		"transactional_or_non":   "li.transactional_or_non",
		"kode_pos":               "li.kode_pos",
		"account_id":             "li.account_id",
		"result_type":            "cr.result_type",
	}

	for filterKey, filterVal := range filters {
		if dbCol, valid := filterColMap[filterKey]; valid && filterVal != "" {
			whereClause += fmt.Sprintf(" AND %s = $%d", dbCol, argIdx)
			args = append(args, filterVal)
			argIdx++
		}
	}

	// Get total count
	var total int
	countQuery := fmt.Sprintf(
		`SELECT COUNT(*) FROM loan_inputs li
		 JOIN cashflow_results cr ON cr.loan_input_id = li.id
		 WHERE %s`, whereClause,
	)
	config.DB.QueryRow(countQuery, args...).Scan(&total)

	// Get paginated results
	query := fmt.Sprintf(
		`SELECT li.row_number, li.reporting_date, li.account_id, li.ccy, li.outstanding,
		        li.interest_rate, li.start_date, li.end_date, li.installment_frequency,
		        li.product_type, li.segment, li.daerah, li.kode_pos,
		        li.insured_or_uninsured, li.transactional_or_non, li.method,
		        li.interest_payment_frequency, li.day_count,
		        cr.remaining_days,
		        cr.irrbb_principal, cr.irrbb_interest,
		        cr.lcr_principal, cr.lcr_interest,
		        cr.nsfr_principal, cr.nsfr_interest,
		        cr.result_type
		 FROM loan_inputs li
		 JOIN cashflow_results cr ON cr.loan_input_id = li.id
		 WHERE %s
		 ORDER BY %s %s
		 LIMIT $%d OFFSET $%d`,
		whereClause, sortCol, sortDir, argIdx, argIdx+1,
	)
	args = append(args, limit, offset)

	rows, err := config.DB.Query(query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query results: " + err.Error()})
		return
	}
	defer rows.Close()

	results := make([]models.ResultRow, 0)
	for rows.Next() {
		var r models.ResultRow
		var reportingDate time.Time
		var startDate sql.NullTime
		var endDate sql.NullTime
		var installmentFreq, interestPaymentFreq sql.NullInt64
		var irrbbPJSON, irrbbIJSON, lcrPJSON, lcrIJSON, nsfrPJSON, nsfrIJSON []byte

		err := rows.Scan(
			&r.RowNumber, &reportingDate, &r.AccountID, &r.CCY, &r.Outstanding,
			&r.InterestRate, &startDate, &endDate, &installmentFreq,
			&r.ProductType, &r.Segment, &r.Daerah, &r.KodePos,
			&r.InsuredOrUninsured, &r.TransactionalOrNon, &r.Method,
			&interestPaymentFreq, &r.DayCount,
			&r.RemainingDays,
			&irrbbPJSON, &irrbbIJSON, &lcrPJSON, &lcrIJSON, &nsfrPJSON, &nsfrIJSON,
			&r.ResultType,
		)
		if err != nil {
			log.Printf("Error scanning row: %v", err)
			continue
		}

		r.ReportingDate = reportingDate.Format("02/01/2006")
		if startDate.Valid {
			r.StartDate = startDate.Time.Format("02/01/2006")
		}
		if endDate.Valid {
			r.EndDate = endDate.Time.Format("02/01/2006")
		}

		if installmentFreq.Valid {
			v := int(installmentFreq.Int64)
			r.InstallmentFrequency = &v
		}
		if interestPaymentFreq.Valid {
			v := int(interestPaymentFreq.Int64)
			r.InterestPaymentFrequency = &v
		}

		json.Unmarshal(irrbbPJSON, &r.IRRBBPrincipal)
		json.Unmarshal(irrbbIJSON, &r.IRRBBInterest)
		json.Unmarshal(lcrPJSON, &r.LCRPrincipal)
		json.Unmarshal(lcrIJSON, &r.LCRInterest)
		json.Unmarshal(nsfrPJSON, &r.NSFRPrincipal)
		json.Unmarshal(nsfrIJSON, &r.NSFRInterest)

		// Apply filter_type transformation
		applyFilterType(&r, filterType)

		results = append(results, r)
	}

	totalPages := (total + limit - 1) / limit

	c.JSON(http.StatusOK, models.PaginatedResponse{
		Data:       results,
		Total:      total,
		Page:       page,
		Limit:      limit,
		TotalPages: totalPages,
	})
}

// applyFilterType merges bucket maps based on filter_type
func applyFilterType(r *models.ResultRow, filterType string) {
	switch filterType {
	case "bbi":
		// Only principal - zero out interest
		r.IRRBBInterest = nil
		r.LCRInterest = nil
		r.NSFRInterest = nil
	case "interest":
		// Only interest - zero out principal
		r.IRRBBPrincipal = nil
		r.LCRPrincipal = nil
		r.NSFRPrincipal = nil
	case "both":
		// Sum principal + interest into principal fields, nil out interest
		r.IRRBBPrincipal = mergeMaps(r.IRRBBPrincipal, r.IRRBBInterest)
		r.LCRPrincipal = mergeMaps(r.LCRPrincipal, r.LCRInterest)
		r.NSFRPrincipal = mergeMaps(r.NSFRPrincipal, r.NSFRInterest)
		r.IRRBBInterest = nil
		r.LCRInterest = nil
		r.NSFRInterest = nil
	}
}

func mergeMaps(a, b map[string]float64) map[string]float64 {
	result := make(map[string]float64)
	for k, v := range a {
		result[k] = v
	}
	for k, v := range b {
		result[k] += v
	}
	// Round
	for k, v := range result {
		result[k] = calculator.Round2(v)
	}
	return result
}

func queryInt(c *gin.Context, key string, def int) int {
	val := c.DefaultQuery(key, "")
	if val == "" {
		return def
	}
	var v int
	fmt.Sscanf(val, "%d", &v)
	if v <= 0 {
		return def
	}
	return v
}
