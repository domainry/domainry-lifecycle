// Package filesystem exposes Lifecycle-owned filesystem artifact adapters.
// Implementations remain internal to the Lifecycle source module.
package filesystem

import internal "github.com/domainry/domainry-lifecycle/internal/infrastructure/artifact/filesystem"

type SubjectStore = internal.SubjectStore

func NewSubjectStore(root string) *SubjectStore { return internal.NewSubjectStore(root) }
