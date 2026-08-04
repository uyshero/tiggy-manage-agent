package appresource

import (
	"fmt"
	"strings"
)

const (
	maxApplicationIDLength = 128
	maxExternalRefLength   = 512
	maxLabelCount          = 32
	maxLabelKeyLength      = 128
	maxLabelValueLength    = 512
)

type Ownership struct {
	AppID       string            `json:"app_id,omitempty"`
	ExternalRef string            `json:"external_ref,omitempty"`
	Labels      map[string]string `json:"labels"`
}

func Normalize(appID, externalRef string, labels map[string]string) (Ownership, error) {
	appID = strings.TrimSpace(appID)
	externalRef = strings.TrimSpace(externalRef)
	if len(appID) > maxApplicationIDLength {
		return Ownership{}, fmt.Errorf("app_id must not exceed %d characters", maxApplicationIDLength)
	}
	if len(externalRef) > maxExternalRefLength {
		return Ownership{}, fmt.Errorf("external_ref must not exceed %d characters", maxExternalRefLength)
	}
	if externalRef != "" && appID == "" {
		return Ownership{}, fmt.Errorf("external_ref requires app_id")
	}
	if len(labels) > maxLabelCount {
		return Ownership{}, fmt.Errorf("labels must contain at most %d entries", maxLabelCount)
	}
	normalizedLabels := make(map[string]string, len(labels))
	for key, value := range labels {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || len(key) > maxLabelKeyLength {
			return Ownership{}, fmt.Errorf("label keys must contain 1 to %d characters", maxLabelKeyLength)
		}
		if len(value) > maxLabelValueLength {
			return Ownership{}, fmt.Errorf("label %q must not exceed %d characters", key, maxLabelValueLength)
		}
		if _, exists := normalizedLabels[key]; exists {
			return Ownership{}, fmt.Errorf("duplicate normalized label key %q", key)
		}
		normalizedLabels[key] = value
	}
	return Ownership{AppID: appID, ExternalRef: externalRef, Labels: normalizedLabels}, nil
}

func CloneLabels(labels map[string]string) map[string]string {
	cloned := make(map[string]string, len(labels))
	for key, value := range labels {
		cloned[key] = value
	}
	return cloned
}
