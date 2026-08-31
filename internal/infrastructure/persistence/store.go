// Package persistence is the Lifecycle persistence composition boundary.
// Concrete repositories remain classified below database/ and artifact/.
package persistence

import (
	"github.com/domainry/domainry-lifecycle-sdk/contract"
	"github.com/domainry/domainry-lifecycle-sdk/modulehost"
	artifactstore "github.com/domainry/domainry-lifecycle/internal/infrastructure/artifact/filesystem"
	database "github.com/domainry/domainry-lifecycle/internal/infrastructure/persistence/database/lifecycle"
)

type (
	LifecycleStore            = database.LifecycleStore
	FileArtifactStore         = database.FileArtifactStore
	FileArtifactStoreOption   = database.FileArtifactStoreOption
	ArchiveWriter             = database.ArchiveWriter
	RelationalCleanupSpec     = database.RelationalCleanupSpec
	RelationalReferenceCheck  = database.RelationalReferenceCheck
	RelationalChildCollection = database.RelationalChildCollection
)

func NewLifecycleStore(host modulehost.Host) LifecycleStore {
	return database.NewLifecycleStore(host)
}

func NewFileArtifactStore(host modulehost.Host, fields contract.UploadFieldCatalog, root string, options ...FileArtifactStoreOption) *FileArtifactStore {
	return database.NewFileArtifactStore(host, fields, root, options...)
}

func WithUploadArtifactReferences(resolver contract.UploadArtifactReferenceResolver) FileArtifactStoreOption {
	return database.WithUploadArtifactReferences(resolver)
}

func WithExpiredUploadReferenceCleaner(cleaner contract.ExpiredUploadReferenceCleaner) FileArtifactStoreOption {
	return database.WithExpiredUploadReferenceCleaner(cleaner)
}

func NewSubjectArtifactStore(root string) contract.SubjectArtifactStore {
	return artifactstore.NewSubjectStore(root)
}

func NewArchiveWriter(host modulehost.Host) ArchiveWriter { return database.NewArchiveWriter(host) }

func NewRelationalOwnerExecutor(host modulehost.Host, owner string, specs ...RelationalCleanupSpec) contract.OwnerLifecycleExecutor {
	return database.NewRelationalOwnerExecutor(host, owner, specs...)
}
