package lifecycle

import (
	sharedartifact "github.com/domainry/domainry-foundation/artifact"
	"github.com/domainry/domainry-lifecycle-sdk/modulehost"
)

// artifactPersistenceHost is internal composition state. External module
// hosts supply only the database primitives and content ports; the module
// factory opens and binds the Foundation SQL store before constructing these
// repositories.
type artifactPersistenceHost interface {
	modulehost.ArtifactStoreHost
	ArtifactStore() sharedartifact.ManagedStore
}
