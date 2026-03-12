package calculator

import (
	"log"

	"bs-be/config"
)

// BehaviourWeights holds {bucket_type: {bucket_name: percentage}}
type BehaviourWeights map[string]map[string]float64

// LoadBehaviourWeights loads bucket percentages for a given behaviour ID from the DB
func LoadBehaviourWeights(behaviourID int64) BehaviourWeights {
	rows, err := config.DB.Query(
		"SELECT bucket_type, bucket_name, percentage FROM behaviour_buckets WHERE behaviour_id = $1",
		behaviourID,
	)
	if err != nil {
		log.Printf("Failed to load behaviour weights for %d: %v", behaviourID, err)
		return nil
	}
	defer rows.Close()

	weights := make(BehaviourWeights)
	for rows.Next() {
		var bucketType, bucketName string
		var pct float64
		if rows.Scan(&bucketType, &bucketName, &pct) == nil {
			if weights[bucketType] == nil {
				weights[bucketType] = make(map[string]float64)
			}
			weights[bucketType][bucketName] = pct
		}
	}

	return weights
}

// LoadDefaultBehaviourID finds the default behaviour's ID
func LoadDefaultBehaviourID() int64 {
	var id int64
	err := config.DB.QueryRow(
		"SELECT id FROM behaviours WHERE is_default = TRUE AND upload_id IS NULL LIMIT 1",
	).Scan(&id)
	if err != nil {
		log.Printf("Default behaviour not found: %v", err)
		return 0
	}
	return id
}

// ComputeBehaviourBuckets computes bucket values using behaviour weights
// This applies to loans without end date (Default Behaviour) or custom scenarios
// Returns: irrbbPrincipal, irrbbInterest, lcrPrincipal, lcrInterest, nsfrPrincipal, nsfrInterest
func ComputeBehaviourBuckets(outstanding float64, weights BehaviourWeights) (
	irrbbP, irrbbI, lcrP, lcrI, nsfrP, nsfrI map[string]float64,
) {
	irrbbP = EmptyBucketMap(IRRBBLabels)
	irrbbI = EmptyBucketMap(IRRBBLabels)
	lcrP = EmptyBucketMap(LCRLabels)
	lcrI = EmptyBucketMap(LCRLabels)
	nsfrP = EmptyBucketMap(NSFRLabels)
	nsfrI = EmptyBucketMap(NSFRLabels)

	if weights == nil {
		return
	}

	// IRRBB — principal = outstanding * weight, interest = 0
	if irrbbWeights, ok := weights["IRRBB"]; ok {
		for label := range irrbbP {
			if w, exists := irrbbWeights[label]; exists {
				irrbbP[label] = Round2(outstanding * w)
			}
		}
	}

	// LCR — principal = outstanding * weight, interest = 0
	if lcrWeights, ok := weights["LCR"]; ok {
		for label := range lcrP {
			if w, exists := lcrWeights[label]; exists {
				lcrP[label] = Round2(outstanding * w)
			}
		}
	}

	// NSFR — principal = outstanding * weight, interest = 0
	if nsfrWeights, ok := weights["NSFR"]; ok {
		for label := range nsfrP {
			if w, exists := nsfrWeights[label]; exists {
				nsfrP[label] = Round2(outstanding * w)
			}
		}
	}

	return
}

// ScenarioBucketConfig holds the parsed Bucket sheet configuration for a scenario
type ScenarioBucketConfig struct {
	ProductType   string
	CCY           string
	Segment       string
	Transactional string
	ValueType     string // "Outstanding" or "Market"
	BucketType    string // "LCR", "NSFR", "IRRBB"
	Config        map[string]float64 // bucket_name -> percentage
}

// ScenarioCashflowAssumption holds the Cashflow Assumption multiplier
type ScenarioCashflowAssumption struct {
	ProductType   string
	CCY           string
	Segment       string
	Transactional string
	BucketType    string
	Percentage    float64
}

// ScenarioData holds parsed scenario data (Bucket + Cashflow Assumption)
type ScenarioData struct {
	BucketConfigs        []ScenarioBucketConfig
	CashflowAssumptions  []ScenarioCashflowAssumption
}

// FindMatchingConfig finds the first bucket config matching loan criteria
func (sd *ScenarioData) FindMatchingConfig(productType, ccy, segment, transactional, bucketType string) *ScenarioBucketConfig {
	for i, cfg := range sd.BucketConfigs {
		if cfg.BucketType != bucketType {
			continue
		}
		if (cfg.ProductType == productType || cfg.ProductType == "All" || cfg.ProductType == "") &&
			(cfg.CCY == ccy || cfg.CCY == "All" || cfg.CCY == "") &&
			(cfg.Segment == segment || cfg.Segment == "All" || cfg.Segment == "") &&
			(cfg.Transactional == transactional || cfg.Transactional == "All" || cfg.Transactional == "") {
			return &sd.BucketConfigs[i]
		}
	}
	return nil
}

// FindCashflowAssumption finds the cashflow assumption multiplier
func (sd *ScenarioData) FindCashflowAssumption(productType, ccy, segment, transactional, bucketType string) float64 {
	for _, ca := range sd.CashflowAssumptions {
		if ca.BucketType != bucketType {
			continue
		}
		if (ca.ProductType == productType || ca.ProductType == "All" || ca.ProductType == "") &&
			(ca.CCY == ccy || ca.CCY == "All" || ca.CCY == "") &&
			(ca.Segment == segment || ca.Segment == "All" || ca.Segment == "") &&
			(ca.Transactional == transactional || ca.Transactional == "All" || ca.Transactional == "") {
			return ca.Percentage
		}
	}
	return 1.0 // default: no multiplier
}

// ComputeScenarioBuckets computes bucket values using scenario config for a single bucket type.
// It looks up the matching config, applies percentage * base_value * cashflow_assumption.
// If no matching config found, returns nil (caller should use fallback).
func ComputeScenarioBucketsForType(
	sd *ScenarioData,
	bucketType string,
	labels []string,
	outstanding float64,
	marketValue float64,
	productType, ccy, segment, transactional string,
) (map[string]float64, bool) {
	cfg := sd.FindMatchingConfig(productType, ccy, segment, transactional, bucketType)
	if cfg == nil {
		return nil, false
	}

	assumption := sd.FindCashflowAssumption(productType, ccy, segment, transactional, bucketType)

	// Determine base value from Value Type
	baseValue := outstanding
	if cfg.ValueType == "Market" {
		if marketValue > 0 {
			baseValue = marketValue
		}
		// if no market value, fallback to outstanding
	}

	result := EmptyBucketMap(labels)
	for label := range result {
		if pct, exists := cfg.Config[label]; exists {
			result[label] = Round2(baseValue * pct * assumption)
		}
	}

	return result, true
}

// ComputeAllScenarioBuckets computes all bucket types for a loan using scenario data.
// Uses fallback values (from amortization or default behaviour) when no scenario config matches.
func ComputeAllScenarioBuckets(
	sd *ScenarioData,
	outstanding float64,
	marketValue float64,
	productType, ccy, segment, transactional string,
	fallbackIRRBBP, fallbackIRRBBI map[string]float64,
	fallbackLCRP, fallbackLCRI map[string]float64,
	fallbackNSFRP, fallbackNSFRI map[string]float64,
) (irrbbP, irrbbI, lcrP, lcrI, nsfrP, nsfrI map[string]float64) {

	// IRRBB
	if result, found := ComputeScenarioBucketsForType(sd, "IRRBB", IRRBBLabels, outstanding, marketValue, productType, ccy, segment, transactional); found {
		irrbbP = result
		irrbbI = EmptyBucketMap(IRRBBLabels) // scenario interest = 0
	} else {
		irrbbP = fallbackIRRBBP
		irrbbI = fallbackIRRBBI
	}

	// LCR
	if result, found := ComputeScenarioBucketsForType(sd, "LCR", LCRLabels, outstanding, marketValue, productType, ccy, segment, transactional); found {
		lcrP = result
		lcrI = EmptyBucketMap(LCRLabels)
	} else {
		lcrP = fallbackLCRP
		lcrI = fallbackLCRI
	}

	// NSFR
	if result, found := ComputeScenarioBucketsForType(sd, "NSFR", NSFRLabels, outstanding, marketValue, productType, ccy, segment, transactional); found {
		nsfrP = result
		nsfrI = EmptyBucketMap(NSFRLabels)
	} else {
		nsfrP = fallbackNSFRP
		nsfrI = fallbackNSFRI
	}

	return
}

// LoadScenarioData loads bucket configs and cashflow assumptions for a behaviour from DB
func LoadScenarioData(behaviourID int64) *ScenarioData {
	sd := &ScenarioData{}

	// Load bucket configs
	rows, err := config.DB.Query(
		`SELECT bucket_type, bucket_name, percentage, 
		        COALESCE(product_type, ''), COALESCE(ccy, ''), 
		        COALESCE(segment, ''), COALESCE(transactional, ''),
		        COALESCE(value_type, 'Outstanding')
		 FROM scenario_bucket_configs WHERE behaviour_id = $1`,
		behaviourID,
	)
	if err != nil {
		log.Printf("Failed to load scenario bucket configs for %d: %v", behaviourID, err)
		return sd
	}
	defer rows.Close()

	// Build grouped configs
	type configKey struct {
		ProductType   string
		CCY           string
		Segment       string
		Transactional string
		ValueType     string
		BucketType    string
	}
	configMap := make(map[configKey]*ScenarioBucketConfig)

	for rows.Next() {
		var bucketType, bucketName string
		var pct float64
		var pt, ccy, seg, trans, vt string
		if rows.Scan(&bucketType, &bucketName, &pct, &pt, &ccy, &seg, &trans, &vt) != nil {
			continue
		}

		key := configKey{pt, ccy, seg, trans, vt, bucketType}
		if configMap[key] == nil {
			configMap[key] = &ScenarioBucketConfig{
				ProductType:   pt,
				CCY:           ccy,
				Segment:       seg,
				Transactional: trans,
				ValueType:     vt,
				BucketType:    bucketType,
				Config:        make(map[string]float64),
			}
		}
		configMap[key].Config[bucketName] = pct
	}

	for _, cfg := range configMap {
		sd.BucketConfigs = append(sd.BucketConfigs, *cfg)
	}

	// Load cashflow assumptions
	rows2, err := config.DB.Query(
		`SELECT COALESCE(product_type, ''), COALESCE(ccy, ''), 
		        COALESCE(segment, ''), COALESCE(transactional, ''),
		        bucket_type, percentage
		 FROM scenario_cashflow_assumptions WHERE behaviour_id = $1`,
		behaviourID,
	)
	if err != nil {
		log.Printf("Failed to load cashflow assumptions for %d: %v", behaviourID, err)
		return sd
	}
	defer rows2.Close()

	for rows2.Next() {
		var ca ScenarioCashflowAssumption
		if rows2.Scan(&ca.ProductType, &ca.CCY, &ca.Segment, &ca.Transactional, &ca.BucketType, &ca.Percentage) == nil {
			sd.CashflowAssumptions = append(sd.CashflowAssumptions, ca)
		}
	}

	return sd
}
