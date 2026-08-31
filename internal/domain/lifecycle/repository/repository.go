// Package repository defines Lifecycle persistence ports. Aliases retain the
// public SDK contract while keeping implementation dependencies classified.
package repository

import sdkpersistence "github.com/domainry/domainry-lifecycle-sdk/persistence"

type (
	PolicyRepository                   = sdkpersistence.PolicyRepository
	LegalHoldRepository                = sdkpersistence.LegalHoldRepository
	CleanupJobRepository               = sdkpersistence.CleanupJobRepository
	SubjectRequestRepository           = sdkpersistence.SubjectRequestRepository
	SubjectRequestTransitionRepository = sdkpersistence.SubjectRequestTransitionRepository
	LifecycleEvidenceRepository        = sdkpersistence.LifecycleEvidenceRepository
	LifecycleRepository                = sdkpersistence.LifecycleRepository
)
