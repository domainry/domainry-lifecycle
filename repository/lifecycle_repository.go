package repository

import (
	"context"
	"time"

	lifecycleaccess "github.com/domainry/domainry-lifecycle/access"
	lifecyclemodel "github.com/domainry/domainry-lifecycle/model"
)

type PolicyRepository interface {
	SavePolicy(context.Context, lifecyclemodel.PolicyVersion) error
	LatestPolicy(context.Context, string, string) (lifecyclemodel.PolicyVersion, bool, error)
	ListPolicies(context.Context, string) ([]lifecyclemodel.PolicyVersion, error)
}

type LegalHoldRepository interface {
	SaveLegalHold(context.Context, lifecyclemodel.LegalHold) error
	GetLegalHold(context.Context, string, string) (lifecyclemodel.LegalHold, bool, error)
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
	GetSubjectRequest(context.Context, string, string) (lifecyclemodel.SubjectRequest, bool, error)
	ExpireSubjectExportReferences(context.Context, lifecycleaccess.SystemScope, time.Time) ([]lifecyclemodel.SubjectRequest, error)
	SaveExternalErasures(context.Context, []lifecyclemodel.ExternalErasure) error
	ListExternalErasures(context.Context, string, string) ([]lifecyclemodel.ExternalErasure, error)
	ReconcileExternalErasure(context.Context, string, string, string, time.Time) (lifecyclemodel.ExternalErasure, bool, error)
	SaveDeletionRegistration(context.Context, lifecyclemodel.DeletionRegistration) error
	ListPendingDeletionRegistrations(context.Context, string, int) ([]lifecyclemodel.DeletionRegistration, error)
}

// SubjectRequestTransitionRepository is an optional compare-and-swap
// capability. Hosts should implement it when concurrent subject-request
// execution is possible; the application retains SaveSubjectRequest fallback
// support for small embedders and test adapters.
type SubjectRequestTransitionRepository interface {
	TransitionSubjectRequest(context.Context, lifecyclemodel.SubjectRequest, lifecyclemodel.SubjectRequest) error
}

type LifecycleEvidenceRepository interface {
	ListArchiveEntries(context.Context, string, string, int) ([]lifecyclemodel.ArchiveEntry, error)
	AppendAuditEvidence(context.Context, lifecyclemodel.AuditEvidence) error
	Metrics(context.Context, string, time.Time) (lifecyclemodel.Metrics, error)
	GlobalMetrics(context.Context, lifecycleaccess.SystemScope, time.Time) (lifecyclemodel.Metrics, error)
}

// LifecycleRepository is the host-side aggregate used by the default database
// adapter. Application services depend on the narrower capability interfaces
// above; embedders may provide those capabilities independently.
type LifecycleRepository interface {
	PolicyRepository
	LegalHoldRepository
	CleanupJobRepository
	SubjectRequestRepository
	LifecycleEvidenceRepository
}
