package handlers

import "testing"

func bc(bucketType, bucketName, productType, ccy, segment, transactional, valueType string) bucketConfigInternal {
	return bucketConfigInternal{
		BucketType:    bucketType,
		BucketName:    bucketName,
		Percentage:    1,
		ProductType:   productType,
		CCY:           ccy,
		Segment:       segment,
		Transactional: transactional,
		ValueType:     valueType,
	}
}

func cfa(productType, ccy, segment, transactional, bucketType string) cashflowAssumptionInternal {
	return cashflowAssumptionInternal{
		ProductType:   productType,
		CCY:           ccy,
		Segment:       segment,
		Transactional: transactional,
		BucketType:    bucketType,
		Percentage:    1,
	}
}

func TestValidateScenario(t *testing.T) {
	tests := []struct {
		name    string
		buckets []bucketConfigInternal
		cfs     []cashflowAssumptionInternal
		wantErr bool
	}{
		{
			// The regression that made every upload fail: one rule spread over
			// many Bucket Name rows is a single rule, not N overlapping ones.
			name: "many bucket names under one rule is valid",
			buckets: []bucketConfigInternal{
				bc("LCR", "CF <= 30D", "1", "IDR", "1", "1", "Outstanding"),
				bc("LCR", "CF > 30D", "1", "IDR", "1", "1", "Outstanding"),
				bc("NSFR", "CF < 6M", "1", "IDR", "1", "1", "Outstanding"),
				bc("NSFR", "CF 6M to 12M", "1", "IDR", "1", "1", "Outstanding"),
				bc("NSFR", "CF > 12M", "1", "IDR", "1", "1", "Outstanding"),
			},
			wantErr: false,
		},
		{
			// The cross-section rule used to reject this; it is the intended
			// configuration (percentage x multiplier).
			name: "bucket and cashflow assumption on identical criteria is valid",
			buckets: []bucketConfigInternal{
				bc("ILAAP", "D-1", "2", "IDR", "2", "2", "Outstanding"),
				bc("ILAAP", "D-2", "2", "IDR", "2", "2", "Outstanding"),
			},
			cfs:     []cashflowAssumptionInternal{cfa("2", "IDR", "2", "2", "ILAAP")},
			wantErr: false,
		},
		{
			name: "distinct criteria in same bucket type is valid",
			buckets: []bucketConfigInternal{
				bc("LCR", "CF <= 30D", "1", "IDR", "1", "1", "Outstanding"),
				bc("LCR", "CF <= 30D", "2", "IDR", "2", "2", "Outstanding"),
			},
			wantErr: false,
		},
		{
			name: "All wildcard colliding with a specific rule is an overlap",
			buckets: []bucketConfigInternal{
				bc("LCR", "CF <= 30D", "All", "IDR", "1", "1", "Outstanding"),
				bc("LCR", "CF <= 30D", "1", "IDR", "1", "1", "Outstanding"),
			},
			wantErr: true,
		},
		{
			// Same criteria, two Value Types: ambiguous, since the first match
			// decides whether the base is Outstanding or Market.
			name: "same criteria with conflicting value types is an overlap",
			buckets: []bucketConfigInternal{
				bc("IRRBB", "<= 1 M", "1", "IDR", "1", "1", "Outstanding"),
				bc("IRRBB", "<= 1 M", "1", "IDR", "1", "1", "Market"),
			},
			wantErr: true,
		},
		{
			name: "exact duplicate row is rejected",
			buckets: []bucketConfigInternal{
				bc("LCR", "CF <= 30D", "1", "IDR", "1", "1", "Outstanding"),
				bc("LCR", "CF <= 30D", "1", "IDR", "1", "1", "Outstanding"),
			},
			wantErr: true,
		},
		{
			name: "overlapping cashflow assumptions are rejected",
			cfs: []cashflowAssumptionInternal{
				cfa("All", "IDR", "1", "1", "ILAAP"),
				cfa("1", "IDR", "1", "1", "ILAAP"),
			},
			wantErr: true,
		},
		{
			name: "cashflow assumptions on different bucket types are valid",
			cfs: []cashflowAssumptionInternal{
				cfa("1", "IDR", "1", "1", "ILAAP"),
				cfa("1", "IDR", "1", "1", "LCR"),
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := validateScenarioCSV(tt.buckets, tt.cfs)
			if got := len(errs) > 0; got != tt.wantErr {
				t.Errorf("wantErr=%v, got %d error(s): %v", tt.wantErr, len(errs), errs)
			}
		})
	}
}
