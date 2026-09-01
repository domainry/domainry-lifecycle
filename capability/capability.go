// Package capability exposes Lifecycle's source-owned capability contract
// without opening persistence or cleanup workers.
package capability

import (
	"github.com/domainry/domainry-foundation/modulecapability"
	lifecyclehttp "github.com/domainry/domainry-lifecycle/internal/transport/http/module"
)

type Inputs struct{}

func Open(Inputs) (*modulecapability.StaticBinding, error) {
	return lifecyclehttp.NewCapabilityBinding()
}
