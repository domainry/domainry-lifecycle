package persistence

import (
	"context"

	sharedsubject "github.com/domainry/domainry-foundation/subjectlifecycle"
	"github.com/domainry/domainry-lifecycle-sdk/modulehost"
	migration "github.com/domainry/domainry-lifecycle/internal/infrastructure/persistence/database/migration"
)

func ApplySchema(ctx context.Context, host modulehost.Host) error {
	if err := sharedsubject.EnsureSchema(ctx, host.Dialect(), host.Migrations()); err != nil {
		return err
	}
	return migration.Apply(ctx, host)
}
