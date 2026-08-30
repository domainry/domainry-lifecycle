// Package lifecycle exposes Lifecycle-owned persistence adapters to embedded
// hosts while keeping their implementation under the source module's internal
// infrastructure tree.
package lifecycle

import (
	"github.com/domainry/domainry-lifecycle-sdk/contract"
	"github.com/domainry/domainry-lifecycle-sdk/modulehost"
	internal "github.com/domainry/domainry-lifecycle/internal/infrastructure/persistence/database/lifecycle"
)

type LifecycleStore = internal.LifecycleStore
type FileArtifactStore = internal.FileArtifactStore
type FileArtifactStoreOption = internal.FileArtifactStoreOption
type RelationalCleanupSpec = internal.RelationalCleanupSpec
type RelationalReferenceCheck = internal.RelationalReferenceCheck
type RelationalChildCollection = internal.RelationalChildCollection
type ArchiveWriter = internal.ArchiveWriter

func NewLifecycleStore(host modulehost.Host) LifecycleStore { return internal.NewLifecycleStore(host) }
func NewFileArtifactStore(host modulehost.Host, fields contract.UploadFieldCatalog, root string, options ...FileArtifactStoreOption) *FileArtifactStore {
	return internal.NewFileArtifactStore(host, fields, root, options...)
}
func WithUploadArtifactReferences(value contract.UploadArtifactReferenceResolver) FileArtifactStoreOption {
	return internal.WithUploadArtifactReferences(value)
}
func WithExpiredUploadReferenceCleaner(value contract.ExpiredUploadReferenceCleaner) FileArtifactStoreOption {
	return internal.WithExpiredUploadReferenceCleaner(value)
}
func NewRelationalOwnerExecutor(host modulehost.Host, owner string, specs ...RelationalCleanupSpec) contract.OwnerLifecycleExecutor {
	return internal.NewRelationalOwnerExecutor(host, owner, specs...)
}
func NewArchiveWriter(host modulehost.Host) ArchiveWriter { return internal.NewArchiveWriter(host) }
