package skillretention

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

const (
	DefaultRetentionDays = 30
	DefaultDeleteLimit   = 100
	MaxRetentionDays     = 3650
	MaxDeleteLimit       = 1000
)

func DefaultPolicy() Policy {
	return Policy{Enabled: false, RetentionDays: DefaultRetentionDays, DeleteLimit: DefaultDeleteLimit}
}

func NormalizePolicy(policy Policy) (Policy, error) {
	if policy.RetentionDays == 0 {
		policy.RetentionDays = DefaultRetentionDays
	}
	if policy.DeleteLimit == 0 {
		policy.DeleteLimit = DefaultDeleteLimit
	}
	if policy.RetentionDays < 1 || policy.RetentionDays > MaxRetentionDays {
		return Policy{}, fmt.Errorf("retention_days must be between 1 and %d", MaxRetentionDays)
	}
	if policy.DeleteLimit < 1 || policy.DeleteLimit > MaxDeleteLimit {
		return Policy{}, fmt.Errorf("delete_limit must be between 1 and %d", MaxDeleteLimit)
	}
	return policy, nil
}

func PolicyRevision(policy Policy) (string, error) {
	normalized, err := NormalizePolicy(policy)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}
