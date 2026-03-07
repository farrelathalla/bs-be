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

// GetMatchingBehaviours returns behaviour IDs and names that match a loan's criteria for a given upload
func GetMatchingBehaviours(uploadID, productType, ccy, segment, transactional string) []struct {
	BehaviourID   int64
	BehaviourName string
} {
	rows, err := config.DB.Query(
		`SELECT sm.behaviour_id, b.name
		 FROM scenario_mappings sm
		 JOIN behaviours b ON b.id = sm.behaviour_id
		 WHERE sm.upload_id = $1
		   AND sm.product_type = $2
		   AND sm.ccy = $3
		   AND sm.segment = $4
		   AND sm.transactional = $5`,
		uploadID, productType, ccy, segment, transactional,
	)
	if err != nil {
		log.Printf("Failed to get matching behaviours: %v", err)
		return nil
	}
	defer rows.Close()

	var results []struct {
		BehaviourID   int64
		BehaviourName string
	}
	for rows.Next() {
		var r struct {
			BehaviourID   int64
			BehaviourName string
		}
		if rows.Scan(&r.BehaviourID, &r.BehaviourName) == nil {
			results = append(results, r)
		}
	}

	return results
}
