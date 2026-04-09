package services

import "strings"

const (
	KeySelectionPolicyFillFirst = "fill_first"
	KeySelectionPolicyBalance   = "balance"
)

func DefaultKeySelectionPolicy() string {
	return KeySelectionPolicyFillFirst
}

func NormalizeKeySelectionPolicy(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", KeySelectionPolicyFillFirst, "fill-first", "fillfirst":
		return KeySelectionPolicyFillFirst
	case KeySelectionPolicyBalance, "balanced", "highest_remaining", "max_remaining", "rr", "round_robin", "round-robin":
		return KeySelectionPolicyBalance
	default:
		return ""
	}
}
