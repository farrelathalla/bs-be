package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"bs-be/calculator"
	"bs-be/config"

	"github.com/gin-gonic/gin"
)

func GetPivot(c *gin.Context) {
	uploadID := c.Param("id")

	filterType := c.DefaultQuery("filter_type", "both")
	pivotKeysStr := c.DefaultQuery("pivot_keys", "")
	filtersJSON := c.DefaultQuery("filters", "{}")

	if pivotKeysStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "pivot_keys is required"})
		return
	}

	pivotKeys := strings.Split(pivotKeysStr, ",")

	// Validate pivot keys
	validPivotCols := map[string]string{
		"ccy":                    "li.ccy",
		"segment":                "li.segment",
		"product_type":           "li.product_type",
		"daerah":                 "li.daerah",
		"method":                 "li.method",
		"insured_or_uninsured":   "li.insured_or_uninsured",
		"transactional_or_non":   "li.transactional_or_non",
		"kode_pos":               "li.kode_pos",
		"account_id":             "li.account_id",
		"reporting_date":         "li.reporting_date::text",
		"day_count":              "li.day_count",
		"installment_frequency":  "li.installment_frequency::text",
		"result_type":            "cr.result_type",
	}

	var selectCols []string
	var groupCols []string
	for _, k := range pivotKeys {
		k = strings.TrimSpace(k)
		dbCol, ok := validPivotCols[k]
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Invalid pivot key: %s", k)})
			return
		}
		alias := k
		selectCols = append(selectCols, fmt.Sprintf("%s AS %s", dbCol, alias))
		groupCols = append(groupCols, dbCol)
	}

	// Parse filters
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
		"result_type":            "cr.result_type",
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

	// Build aggregation query using JSONB functions
	// We'll aggregate the bucket values in Go after fetching grouped raw sums
	query := fmt.Sprintf(
		`SELECT %s,
		        COUNT(*) as cnt,
		        SUM(li.outstanding) as total_outstanding,
		        SUM(cr.remaining_days) as total_remaining_days,
		        jsonb_object_agg_sum(cr.irrbb_principal) as agg_irrbb_p,
		        jsonb_object_agg_sum(cr.irrbb_interest) as agg_irrbb_i,
		        jsonb_object_agg_sum(cr.lcr_principal) as agg_lcr_p,
		        jsonb_object_agg_sum(cr.lcr_interest) as agg_lcr_i,
		        jsonb_object_agg_sum(cr.nsfr_principal) as agg_nsfr_p,
		        jsonb_object_agg_sum(cr.nsfr_interest) as agg_nsfr_i
		 FROM loan_inputs li
		 JOIN cashflow_results cr ON cr.loan_input_id = li.id
		 WHERE %s
		 GROUP BY %s
		 ORDER BY %s`,
		strings.Join(selectCols, ", "),
		whereClause,
		strings.Join(groupCols, ", "),
		strings.Join(groupCols, ", "),
	)

	// The jsonb_object_agg_sum is a custom aggregate - we need a different approach
	// Let's use a simpler method: fetch all bucket JSONs and aggregate in Go
	simpleQuery := fmt.Sprintf(
		`SELECT %s,
		        COUNT(*) as cnt,
		        SUM(li.outstanding) as total_outstanding,
		        SUM(cr.remaining_days) as total_remaining_days
		 FROM loan_inputs li
		 JOIN cashflow_results cr ON cr.loan_input_id = li.id
		 WHERE %s
		 GROUP BY %s
		 ORDER BY %s`,
		strings.Join(selectCols, ", "),
		whereClause,
		strings.Join(groupCols, ", "),
		strings.Join(groupCols, ", "),
	)

	_ = query // unused, we use simpleQuery + bucket aggregation below

	// First get the group keys and counts
	rows, err := config.DB.Query(simpleQuery, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query pivot: " + err.Error()})
		return
	}
	defer rows.Close()

	type groupInfo struct {
		Keys             map[string]string
		Count            int
		TotalOutstanding float64
		TotalRemDays     int
	}

	var groups []groupInfo
	for rows.Next() {
		// Dynamically scan pivot key values
		vals := make([]interface{}, len(pivotKeys)+3)
		valPtrs := make([]interface{}, len(pivotKeys)+3)
		strVals := make([]string, len(pivotKeys))

		for i := range pivotKeys {
			valPtrs[i] = &strVals[i]
		}

		var cnt int
		var totalOuts float64
		var totalRem int
		valPtrs[len(pivotKeys)] = &cnt
		valPtrs[len(pivotKeys)+1] = &totalOuts
		valPtrs[len(pivotKeys)+2] = &totalRem

		_ = vals

		if err := rows.Scan(valPtrs...); err != nil {
			continue
		}

		keys := make(map[string]string)
		for i, k := range pivotKeys {
			keys[k] = strVals[i]
		}

		groups = append(groups, groupInfo{
			Keys:             keys,
			Count:            cnt,
			TotalOutstanding: totalOuts,
			TotalRemDays:     totalRem,
		})
	}

	// Now for each group, get aggregated bucket values
	var result []gin.H
	for _, g := range groups {
		// Build where clause for this group
		groupWhere := whereClause
		groupArgs := make([]interface{}, len(args))
		copy(groupArgs, args)
		gArgIdx := argIdx

		for _, k := range pivotKeys {
			dbCol, _ := validPivotCols[k]
			groupWhere += fmt.Sprintf(" AND %s = $%d", dbCol, gArgIdx)
			groupArgs = append(groupArgs, g.Keys[k])
			gArgIdx++
		}

		// Get all bucket JSONs for this group
		bucketQuery := fmt.Sprintf(
			`SELECT cr.irrbb_principal, cr.irrbb_interest,
			        cr.lcr_principal, cr.lcr_interest,
			        cr.nsfr_principal, cr.nsfr_interest
			 FROM loan_inputs li
			 JOIN cashflow_results cr ON cr.loan_input_id = li.id
			 WHERE %s`, groupWhere,
		)

		bucketRows, err := config.DB.Query(bucketQuery, groupArgs...)
		if err != nil {
			continue
		}

		// Aggregate buckets
		aggIrrbbP := calculator.EmptyBucketMap(calculator.IRRBBLabels)
		aggIrrbbI := calculator.EmptyBucketMap(calculator.IRRBBLabels)
		aggLcrP := calculator.EmptyBucketMap(calculator.LCRLabels)
		aggLcrI := calculator.EmptyBucketMap(calculator.LCRLabels)
		aggNsfrP := calculator.EmptyBucketMap(calculator.NSFRLabels)
		aggNsfrI := calculator.EmptyBucketMap(calculator.NSFRLabels)

		for bucketRows.Next() {
			var ipJ, iiJ, lpJ, liJ, npJ, niJ []byte
			if err := bucketRows.Scan(&ipJ, &iiJ, &lpJ, &liJ, &npJ, &niJ); err != nil {
				continue
			}

			addJSONToMap(aggIrrbbP, ipJ)
			addJSONToMap(aggIrrbbI, iiJ)
			addJSONToMap(aggLcrP, lpJ)
			addJSONToMap(aggLcrI, liJ)
			addJSONToMap(aggNsfrP, npJ)
			addJSONToMap(aggNsfrI, niJ)
		}
		bucketRows.Close()

		// Apply filter type
		aggregates := buildAggregates(aggIrrbbP, aggIrrbbI, aggLcrP, aggLcrI, aggNsfrP, aggNsfrI, filterType)
		aggregates["outstanding"] = calculator.Round2(g.TotalOutstanding)
		aggregates["remaining_days"] = float64(g.TotalRemDays)

		result = append(result, gin.H{
			"keys":       g.Keys,
			"count":      g.Count,
			"aggregates": aggregates,
		})
	}

	if result == nil {
		result = []gin.H{}
	}

	c.JSON(http.StatusOK, result)
}

func addJSONToMap(target map[string]float64, jsonData []byte) {
	var m map[string]float64
	if err := json.Unmarshal(jsonData, &m); err != nil {
		return
	}
	for k, v := range m {
		target[k] += v
	}
}

func buildAggregates(irrbbP, irrbbI, lcrP, lcrI, nsfrP, nsfrI map[string]float64, filterType string) map[string]float64 {
	result := make(map[string]float64)

	switch filterType {
	case "bbi":
		for k, v := range irrbbP {
			result["irrbb__"+k] = calculator.Round2(v)
		}
		for k, v := range lcrP {
			result["lcr__"+k] = calculator.Round2(v)
		}
		for k, v := range nsfrP {
			result["nsfr__"+k] = calculator.Round2(v)
		}
	case "interest":
		for k, v := range irrbbI {
			result["irrbb__"+k] = calculator.Round2(v)
		}
		for k, v := range lcrI {
			result["lcr__"+k] = calculator.Round2(v)
		}
		for k, v := range nsfrI {
			result["nsfr__"+k] = calculator.Round2(v)
		}
	default: // both
		for k, v := range irrbbP {
			result["irrbb__"+k] = calculator.Round2(v + irrbbI[k])
		}
		for k, v := range lcrP {
			result["lcr__"+k] = calculator.Round2(v + lcrI[k])
		}
		for k, v := range nsfrP {
			result["nsfr__"+k] = calculator.Round2(v + nsfrI[k])
		}
	}

	return result
}

// GetFilterOptions returns unique values for filterable columns
func GetFilterOptions(c *gin.Context) {
	uploadID := c.Param("id")
	column := c.Query("column")

	validInputCols := map[string]string{
		"ccy":                        "ccy",
		"segment":                    "segment",
		"product_type":                 "product_type",
		"daerah":                       "daerah",
		"method":                       "method",
		"insured_or_uninsured":         "insured_or_uninsured",
		"transactional_or_non":         "transactional_or_non",
		"kode_pos":                     "kode_pos",
		"day_count":                    "day_count",
		"reporting_date":               "reporting_date::text",
		"account_id":                   "account_id",
		"start_date":                   "start_date::text",
		"end_date":                     "end_date::text",
		"outstanding":                  "outstanding::text",
		"interest_rate":                "interest_rate::text",
		"installment_frequency":        "installment_frequency::text",
		"interest_payment_frequency":   "interest_payment_frequency::text",
	}

	validResultCols := map[string]string{
		"result_type":    "result_type",
		"remaining_days": "remaining_days::text",
	}

	var query string
	if dbCol, ok := validInputCols[column]; ok {
		query = fmt.Sprintf(
			"SELECT DISTINCT %s FROM loan_inputs WHERE upload_id = $1 AND %s IS NOT NULL ORDER BY %s",
			dbCol, dbCol, dbCol,
		)
	} else if dbCol, ok := validResultCols[column]; ok {
		query = fmt.Sprintf(
			"SELECT DISTINCT %s FROM cashflow_results WHERE upload_id = $1 AND %s IS NOT NULL ORDER BY %s",
			dbCol, dbCol, dbCol,
		)
	} else {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid column"})
		return
	}

	rows, err := config.DB.Query(query, uploadID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query values"})
		return
	}
	defer rows.Close()

	var values []string
	for rows.Next() {
		var v string
		if rows.Scan(&v) == nil {
			values = append(values, v)
		}
	}

	if values == nil {
		values = []string{}
	}

	c.JSON(http.StatusOK, values)
}

// GetSummary returns summary statistics for an upload
func GetSummary(c *gin.Context) {
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

	// Get aggregate sums
	var totalCount int
	var totalOutstanding float64

	config.DB.QueryRow(
		fmt.Sprintf("SELECT COUNT(*), COALESCE(SUM(li.outstanding), 0) FROM loan_inputs li WHERE %s", whereClause),
		args...,
	).Scan(&totalCount, &totalOutstanding)

	// Get unique currencies
	currQuery := fmt.Sprintf("SELECT DISTINCT li.ccy FROM loan_inputs li WHERE %s ORDER BY li.ccy", whereClause)
	currRows, _ := config.DB.Query(currQuery, args...)
	var currencies []string
	if currRows != nil {
		defer currRows.Close()
		for currRows.Next() {
			var c string
			if currRows.Scan(&c) == nil {
				currencies = append(currencies, c)
			}
		}
	}
	if currencies == nil {
		currencies = []string{}
	}

	// Get aggregated bucket totals
	bucketQuery := fmt.Sprintf(
		`SELECT cr.irrbb_principal, cr.irrbb_interest,
		        cr.lcr_principal, cr.lcr_interest,
		        cr.nsfr_principal, cr.nsfr_interest
		 FROM loan_inputs li
		 JOIN cashflow_results cr ON cr.loan_input_id = li.id
		 WHERE %s`, whereClause,
	)

	bucketRows, _ := config.DB.Query(bucketQuery, args...)
	aggIrrbbP := calculator.EmptyBucketMap(calculator.IRRBBLabels)
	aggIrrbbI := calculator.EmptyBucketMap(calculator.IRRBBLabels)
	aggLcrP := calculator.EmptyBucketMap(calculator.LCRLabels)
	aggLcrI := calculator.EmptyBucketMap(calculator.LCRLabels)
	aggNsfrP := calculator.EmptyBucketMap(calculator.NSFRLabels)
	aggNsfrI := calculator.EmptyBucketMap(calculator.NSFRLabels)

	if bucketRows != nil {
		defer bucketRows.Close()
		for bucketRows.Next() {
			var ipJ, iiJ, lpJ, liJ, npJ, niJ []byte
			if bucketRows.Scan(&ipJ, &iiJ, &lpJ, &liJ, &npJ, &niJ) == nil {
				addJSONToMap(aggIrrbbP, ipJ)
				addJSONToMap(aggIrrbbI, iiJ)
				addJSONToMap(aggLcrP, lpJ)
				addJSONToMap(aggLcrI, liJ)
				addJSONToMap(aggNsfrP, npJ)
				addJSONToMap(aggNsfrI, niJ)
			}
		}
	}

	bucketTotals := buildAggregates(aggIrrbbP, aggIrrbbI, aggLcrP, aggLcrI, aggNsfrP, aggNsfrI, filterType)

	c.JSON(http.StatusOK, gin.H{
		"total_count":      totalCount,
		"total_outstanding": calculator.Round2(totalOutstanding),
		"currencies":       currencies,
		"bucket_totals":    bucketTotals,
	})
}
