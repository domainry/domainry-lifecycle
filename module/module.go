// Package module exposes the stable in-process Lifecycle module factory.
// Implementation details remain under internal/assembly.
package module

import moduleassembly "github.com/domainry/domainry-lifecycle/internal/assembly/module"

type Options = moduleassembly.Options
type Factory = moduleassembly.Factory

func OptionsFromEnvironment() Options { return moduleassembly.OptionsFromEnvironment() }

func NewFactory(options ...Options) *Factory {
	if len(options) == 0 {
		return moduleassembly.NewFactory(OptionsFromEnvironment())
	}
	return moduleassembly.NewFactory(options[0])
}
