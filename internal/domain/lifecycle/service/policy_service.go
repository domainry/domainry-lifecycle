// Package service exposes Lifecycle domain policy through the source-owned
// domain boundary while preserving the versioned SDK model identity.
package service

import (
	"time"

	"github.com/domainry/domainry-lifecycle-sdk/policy"
	"github.com/domainry/domainry-lifecycle/internal/domain/lifecycle/model"
)

func ValidateRetentionPolicy(value model.RetentionPolicy) error {
	return policy.ValidateRetentionPolicy(value)
}

func EffectiveWorkspaceRetention(value model.RetentionPolicy, override model.WorkspaceRetentionOverride) (time.Duration, error) {
	return policy.EffectiveWorkspaceRetention(value, override)
}

func ValidatePolicyPublication(previous *model.PolicyVersion, next model.PolicyVersion) error {
	return policy.ValidatePolicyPublication(previous, next)
}

func ValidateCleanupJob(value model.CleanupJob) error { return policy.ValidateCleanupJob(value) }

func TransitionSubjectRequest(current, next model.SubjectRequest) error {
	return policy.TransitionSubjectRequest(current, next)
}

func LegalHoldActive(value model.LegalHold, now time.Time) bool {
	return policy.LegalHoldActive(value, now)
}

func EvaluateEligibility(input model.EligibilityInput) (model.EligibilityDecision, error) {
	return policy.EvaluateEligibility(input)
}

func ValidateLegalHold(value model.LegalHold) error { return policy.ValidateLegalHold(value) }

func DefaultPolicyCatalog(workspaceID, publisher string, now time.Time) []model.PolicyVersion {
	return policy.DefaultPolicyCatalog(workspaceID, publisher, now)
}
