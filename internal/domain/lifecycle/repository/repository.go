package repository

import (
	"context"
	"sort"
	"strings"
	"time"

	lifecycleaccess "github.com/domainry/domainry-lifecycle-sdk/access"
	lifecyclemodel "github.com/domainry/domainry-lifecycle/internal/domain/lifecycle/model"
)

// DataScopeFilter is the already-compiled execution constraint for one exact
// Permission. It is not role authoring state and is never persisted by
// Lifecycle. The Identity-owned data_scope vocabulary remains the sole policy
// contract; repositories receive only trusted concrete owner values.
type DataScopeFilter struct {
	Unrestricted bool
	OwnerUserIDs []string
	OwnerOrgIDs  []string
	SubjectOrgID string
}

func UnrestrictedDataScopeFilter() DataScopeFilter {
	return DataScopeFilter{Unrestricted: true}
}

func (filter DataScopeFilter) Normalized() DataScopeFilter {
	filter.OwnerUserIDs = normalizedScopeValues(filter.OwnerUserIDs)
	filter.OwnerOrgIDs = normalizedScopeValues(filter.OwnerOrgIDs)
	filter.SubjectOrgID = strings.TrimSpace(filter.SubjectOrgID)
	return filter
}

func (filter DataScopeFilter) Allows(ownerUserID, ownerOrgID string) bool {
	filter = filter.Normalized()
	if filter.Unrestricted {
		return true
	}
	return scopeContains(filter.OwnerUserIDs, ownerUserID) || scopeContains(filter.OwnerOrgIDs, ownerOrgID)
}

func normalizedScopeValues(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			seen[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func scopeContains(values []string, expected string) bool {
	expected = strings.TrimSpace(expected)
	if expected == "" {
		return false
	}
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

type PolicyRepository interface {
	SavePolicy(context.Context, lifecyclemodel.PolicyVersion) error
	LatestPolicy(context.Context, string, string, DataScopeFilter) (lifecyclemodel.PolicyVersion, bool, error)
	ListPolicies(context.Context, string, DataScopeFilter) ([]lifecyclemodel.PolicyVersion, error)
}

type LegalHoldRepository interface {
	SaveLegalHold(context.Context, lifecyclemodel.LegalHold) error
	ListLegalHolds(context.Context, string, int, DataScopeFilter) ([]lifecyclemodel.LegalHold, error)
	GetLegalHold(context.Context, string, string, DataScopeFilter) (lifecyclemodel.LegalHold, bool, error)
	UpdateLegalHold(context.Context, lifecyclemodel.LegalHold, DataScopeFilter) (bool, error)
	ActiveLegalHolds(context.Context, lifecyclemodel.ResourceTarget, time.Time) ([]lifecyclemodel.LegalHold, error)
}

type CleanupJobRepository interface {
	SaveCleanupJob(context.Context, lifecyclemodel.CleanupJob) error
	GetCleanupJob(context.Context, string, string) (lifecyclemodel.CleanupJob, bool, error)
	ListRunnableCleanupJobs(context.Context, lifecycleaccess.SystemScope, int, time.Time) ([]lifecyclemodel.CleanupJob, error)
	ClaimCleanupJob(context.Context, string, string, string, time.Duration, time.Time) (lifecyclemodel.CleanupJob, bool, error)
	UpdateCleanupJob(context.Context, lifecyclemodel.CleanupJob) error
}

type SubjectRequestRepository interface {
	SaveSubjectRequest(context.Context, lifecyclemodel.SubjectRequest) error
	GetSubjectRequest(context.Context, string, string, DataScopeFilter) (lifecyclemodel.SubjectRequest, bool, error)
	ListSubjectRequests(context.Context, string, int, DataScopeFilter) ([]lifecyclemodel.SubjectRequest, error)
	TransitionSubjectRequest(context.Context, lifecyclemodel.SubjectRequest, lifecyclemodel.SubjectRequest, DataScopeFilter) error
	ExpireSubjectExportReferences(context.Context, lifecycleaccess.SystemScope, time.Time) ([]lifecyclemodel.SubjectRequest, error)
	SaveExternalErasures(context.Context, []lifecyclemodel.ExternalErasure) error
	ListExternalErasures(context.Context, string, string, DataScopeFilter) ([]lifecyclemodel.ExternalErasure, error)
	ReconcileExternalErasure(context.Context, string, string, string, time.Time, DataScopeFilter) (lifecyclemodel.ExternalErasure, bool, error)
	SaveDeletionRegistration(context.Context, lifecyclemodel.DeletionRegistration) error
	ListPendingDeletionRegistrations(context.Context, string, int, DataScopeFilter) ([]lifecyclemodel.DeletionRegistration, error)
	ListSubjectExecutionSteps(context.Context, string, string) ([]lifecyclemodel.SubjectExecutionStep, error)
	SaveSubjectExecutionStep(context.Context, lifecyclemodel.SubjectExecutionStep) error
}

type LifecycleEvidenceRepository interface {
	ListArchiveEntries(context.Context, string, string, int, DataScopeFilter) ([]lifecyclemodel.ArchiveEntry, error)
	AppendAuditEvidence(context.Context, lifecyclemodel.AuditEvidence) error
	Metrics(context.Context, string, time.Time, DataScopeFilter) (lifecyclemodel.Metrics, error)
	GlobalMetrics(context.Context, lifecycleaccess.SystemScope, time.Time) (lifecyclemodel.Metrics, error)
}
