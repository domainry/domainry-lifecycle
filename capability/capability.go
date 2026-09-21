// Package capability exposes Lifecycle's source-owned capability contract
// without opening persistence or cleanup workers.
package capability

import (
	"github.com/domainry/domainry-foundation/modulecapability"
)

type Inputs struct{}

func Open(inputs Inputs) (*modulecapability.StaticBinding, error) {
	return openContract(inputs)
}
