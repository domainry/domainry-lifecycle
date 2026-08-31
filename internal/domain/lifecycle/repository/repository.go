// Package repository defines Lifecycle persistence ports. Aliases retain the
// public SDK contract while keeping implementation dependencies classified.
package repository

import sdkrepository "github.com/domainry/domainry-lifecycle-sdk/repository"

type (
	PolicyRepository                   = sdkrepository.PolicyRepository
	LegalHoldRepository                = sdkrepository.LegalHoldRepository
	CleanupJobRepository               = sdkrepository.CleanupJobRepository
	SubjectRequestRepository           = sdkrepository.SubjectRequestRepository
	SubjectRequestTransitionRepository = sdkrepository.SubjectRequestTransitionRepository
	LifecycleEvidenceRepository        = sdkrepository.LifecycleEvidenceRepository
	LifecycleRepository                = sdkrepository.LifecycleRepository
)
