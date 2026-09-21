// Package persistence is the Lifecycle persistence composition boundary.
// Concrete repositories remain classified below database/ and artifact/.
package persistence

import (
	"github.com/domainry/domainry-lifecycle-sdk/contract"
	"github.com/domainry/domainry-lifecycle-sdk/modulehost"
	database "github.com/domainry/domainry-lifecycle/internal/infrastructure/persistence/database/lifecycle"
)

type (
	LifecycleStore          = database.LifecycleStore
	FileArtifactStore       = database.FileArtifactStore
	FileArtifactStoreOption = database.FileArtifactStoreOption
	ArchiveWriter           = database.ArchiveWriter
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

func WithArtifactContentStore(content contract.ArtifactContentStore) FileArtifactStoreOption {
	return database.WithArtifactContentStore(content)
}

func NewSubjectArtifactStore(host modulehost.Host, root string, content ...contract.ArtifactContentStore) contract.SubjectArtifactStore {
	return database.NewSubjectArtifactStore(host, root, content...)
}

func NewArchiveWriter(host modulehost.Host) ArchiveWriter { return database.NewArchiveWriter(host) }
