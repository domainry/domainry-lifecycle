# domainry-lifecycle

Reusable, in-process data lifecycle governance for Domainry SaaS hosts.

The module owns its `lifecycle_*` tables and submits ORM-rendered migrations to
the host. It borrows the host database, SQL dialect renderer, transaction
boundary, migration lock, and the single `_schema_migrations` ledger. It never
opens a database and never creates a module-specific migration ledger.

Hosts depend on `github.com/domainry/domainry-lifecycle-sdk` and inject
`module.NewFactory()` only at the composition root. The resulting SDK Binding
is the sole access path to the Lifecycle repository, artifact stores,
transactions, and archive writer; persistence packages are internal.

Source modules retain ownership of their business tables and register narrow
cleanup, export, and erasure adapters through SDK contracts.

## Source layout

Lifecycle follows the same internal layering as other Domainry source modules:

- `internal/domain/lifecycle` owns domain-policy boundaries;
- `internal/application/lifecycle` coordinates use cases and host transactions;
- `internal/adapter/lifecyclesdk` implements the public SDK binding;
- `internal/assembly/module` is the embedded composition root;
- `internal/infrastructure/persistence` owns ORM-backed storage, schema, and
  host-registered migrations;
- `module` is the only public implementation entrypoint and remains a thin
  facade over internal assembly.

The `assembly/saas` and HTTP transport boundaries are present to keep the
layout stable, but intentionally expose no runtime behavior: lifecycle-sdk
v0.1.5 defines embedded module deployment only. Adding a standalone server
requires a versioned SaaS binding and transport contract first.
