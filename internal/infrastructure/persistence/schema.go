package persistence

import (
	"context"

	"github.com/domainry/domainry-lifecycle-sdk/modulehost"
	migration "github.com/domainry/domainry-lifecycle/internal/infrastructure/persistence/database/migration"
)

func ApplySchema(ctx context.Context, host modulehost.Host) error {
	return migration.Apply(ctx, host)
}
