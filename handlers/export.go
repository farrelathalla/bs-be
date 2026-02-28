package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"bs-be/calculator"
	"bs-be/config"

	"github.com/gin-gonic/gin"
	"github.com/xuri/excelize/v2"
)

func ExportExcel(c *gin.Context) {
	uploadID := c.Param("id")

	filterType := c.DefaultQuery("filter_type", "both")
	filtersJSON := c.DefaultQuery("filters", "{}")

	var filters map[string]string
	json.Unmarshal([]byte(filtersJSON), &filters)

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
	}

	whereClause := "li.upload_id = $1"
	args := []interface{}{uploadID}
	argIdx := 2

	for filterKey, filterVal := range filters {
		if dbCol, valid := filterColMap[filterKey]; valid && filterVal != "" {
			whereClause += fmt.Sprintf(" AND %s = $%d", dbCol, argIdx)
			args = append(args, filterVal)
			argIdx++
		}
	}

	query := fmt.Sprintf(
		`SELECT li.row_number, li.reporting_date, li.account_id, li.ccy, li.outstanding,
		        li.interest_rate, li.start_date, li.end_date, li.installment_frequency,
		        li.product_type, li.segment, li.daerah, li.kode_pos,
		        li.insured_or_uninsured, li.transactional_or_non, li.method,
		        li.interest_payment_frequency, li.day_count,
		        cr.remaining_days,
		        cr.irrbb_principal, cr.irrbb_interest,
		        cr.lcr_principal, cr.lcr_interest,
		        cr.nsfr_principal, cr.nsfr_interest
		 FROM loan_inputs li
		 JOIN cashflow_results cr ON cr.loan_input_id = li.id
		 WHERE %s
		 ORDER BY li.row_number`,
		whereClause,
	)

	rows, err := config.DB.Query(query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query"})
		return
	}
	defer rows.Close()

	f := excelize.NewFile()
	sheet := "Cashflow"
	f.SetSheetName("Sheet1", sheet)

	// Build headers
	headers := []string{
		"Row", "Reporting Date", "Account ID", "CCY", "Outstanding",
		"Interest Rate", "Start Date", "End Date", "Installment Frequency",
		"Product Type", "Segment", "Daerah", "KodePos",
		"Insured/Uninsured", "Transactional/Non", "Method",
		"Interest Payment Frequency", "Day Count", "Remaining Days",
	}

	// Add bucket headers
	for _, l := range calculator.LCRLabels {
		headers = append(headers, "LCR "+l)
	}
	for _, l := range calculator.NSFRLabels {
		headers = append(headers, "NSFR "+l)
	}
	for _, l := range calculator.IRRBBLabels {
		headers = append(headers, "IRRBB "+l)
	}

	// Write headers
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(sheet, cell, h)
	}
	// Style headers
	headerStyle, _ := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true, Size: 10},
		Fill: excelize.Fill{Type: "pattern", Color: []string{"#4472C4"}, Pattern: 1},
	})
	if headerStyle != 0 {
		endCol, _ := excelize.CoordinatesToCellName(len(headers), 1)
		f.SetCellStyle(sheet, "A1", endCol, headerStyle)
	}

	rowIdx := 2
	for rows.Next() {
		var rowNum, remainingDays int
		var reportingDate, startDate, endDate time.Time
		var accountID, ccy, productType, segment, daerah, kodePos string
		var insured, transactional, method, dayCount string
		var outstanding, interestRate float64
		var installmentFreq, interestPaymentFreq sql.NullInt64
		var irrbbPJSON, irrbbIJSON, lcrPJSON, lcrIJSON, nsfrPJSON, nsfrIJSON []byte

		err := rows.Scan(
			&rowNum, &reportingDate, &accountID, &ccy, &outstanding,
			&interestRate, &startDate, &endDate, &installmentFreq,
			&productType, &segment, &daerah, &kodePos,
			&insured, &transactional, &method,
			&interestPaymentFreq, &dayCount,
			&remainingDays,
			&irrbbPJSON, &irrbbIJSON, &lcrPJSON, &lcrIJSON, &nsfrPJSON, &nsfrIJSON,
		)
		if err != nil {
			log.Printf("Export scan error: %v", err)
			continue
		}

		// Parse bucket maps
		var irrbbP, irrbbI, lcrP, lcrI, nsfrP, nsfrI map[string]float64
		json.Unmarshal(irrbbPJSON, &irrbbP)
		json.Unmarshal(irrbbIJSON, &irrbbI)
		json.Unmarshal(lcrPJSON, &lcrP)
		json.Unmarshal(lcrIJSON, &lcrI)
		json.Unmarshal(nsfrPJSON, &nsfrP)
		json.Unmarshal(nsfrIJSON, &nsfrI)

		// Apply filter type
		var lcrVals, nsfrVals, irrbbVals map[string]float64
		switch filterType {
		case "bbi":
			lcrVals, nsfrVals, irrbbVals = lcrP, nsfrP, irrbbP
		case "interest":
			lcrVals, nsfrVals, irrbbVals = lcrI, nsfrI, irrbbI
		default:
			lcrVals = mergeMaps(lcrP, lcrI)
			nsfrVals = mergeMaps(nsfrP, nsfrI)
			irrbbVals = mergeMaps(irrbbP, irrbbI)
		}

		col := 1
		setCell := func(val interface{}) {
			cell, _ := excelize.CoordinatesToCellName(col, rowIdx)
			f.SetCellValue(sheet, cell, val)
			col++
		}

		setCell(rowNum)
		setCell(reportingDate.Format("02/01/2006"))
		setCell(accountID)
		setCell(ccy)
		setCell(outstanding)
		setCell(interestRate)
		setCell(startDate.Format("02/01/2006"))
		setCell(endDate.Format("02/01/2006"))
		if installmentFreq.Valid {
			setCell(int(installmentFreq.Int64))
		} else {
			setCell("")
		}
		setCell(productType)
		setCell(segment)
		setCell(daerah)
		setCell(kodePos)
		setCell(insured)
		setCell(transactional)
		setCell(method)
		if interestPaymentFreq.Valid {
			setCell(int(interestPaymentFreq.Int64))
		} else {
			setCell("")
		}
		setCell(dayCount)
		setCell(remainingDays)

		for _, l := range calculator.LCRLabels {
			setCell(lcrVals[l])
		}
		for _, l := range calculator.NSFRLabels {
			setCell(nsfrVals[l])
		}
		for _, l := range calculator.IRRBBLabels {
			setCell(irrbbVals[l])
		}

		rowIdx++
	}

	// Get filename
	var filename string
	config.DB.QueryRow("SELECT filename FROM uploads WHERE id = $1", uploadID).Scan(&filename)
	if filename == "" {
		filename = "export"
	}

	exportName := fmt.Sprintf("cashflow_%s_%s.xlsx", filterType, filename)

	c.Header("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", exportName))

	if err := f.Write(c.Writer); err != nil {
		log.Printf("Failed to write excel: %v", err)
	}
}
