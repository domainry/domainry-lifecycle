# domainry-lifecycle

Reusable, in-process data-lifecycle governance for Domainry hosts.

Lifecycle owns its domain policy, application use cases, `_lifecycle_*` tables,
module HTTP product surface, and local maintenance tick. It borrows the host
database, SQL dialect renderer, transaction boundary, migration lock, identity
middleware, listeners, and the single `_schema_migrations` ledger. It never
opens a database or starts another server.

Hosts depend on `github.com/domainry/domainry-lifecycle-sdk` and inject
`module.NewFactory()` only at the composition root. After `BindOwners`, the SDK
Binding exposes narrow `Governance`, `System`, and `LocalWorkers` capabilities;
it does not expose Lifecycle repositories or a transaction escape hatch.

Source modules retain ownership of their business tables and register cleanup,
subject-resolution, export, and erasure adapters through SDK contracts. Subject
side effects receive the Lifecycle request ID as their idempotency identity.
Lifecycle records completed owner steps in its own schema so a failed request
can resume without rerunning owners that already completed.

## HTTP ownership

The embedded module contributes 17 authenticated tenant-admin/operations routes
through `modulehttp.Surface`, including policy, legal-hold, cleanup creation and
preview, metrics, archive evidence, subject requests, external-erasure
reconciliation, and deletion replay. Lifecycle owns their request DTOs,
redaction, permissions, governance metadata, and OpenAPI operations.

The host still owns authentication/listener mounting and common transport
governance. A Runtime host may additionally own orchestration endpoints that
span Runtime infrastructure. In domainry-runtime, only
`POST /operations/lifecycle/cleanup/jobs/{jobID}/run` stays Runtime-owned because
it creates/replays a durable Operations receipt before invoking Lifecycle.

## Source layout

- `internal/domain/lifecycle` owns models, repository ports, invariants, state
  transitions, eligibility, and default-policy construction.
- `internal/application/lifecycle` coordinates Lifecycle use cases, audit
  evidence, transactions, workers, and crash recovery.
- `internal/adapter/lifecyclesdk` converts between internal values and the
  stable public SDK contracts.
- `internal/transport/http/module` owns the embedded product HTTP surface and
  OpenAPI operations.
- `internal/infrastructure/persistence` owns ORM-backed storage, artifacts,
  schema, and host-registered migrations.
- `internal/assembly/module` and public `module` form the embedded composition
  root and its thin implementation entrypoint.

`internal/assembly/saas`, `internal/transport/http/saas`, and
`cmd/lifecycle-server` remain empty boundaries. A standalone deployment needs a
versioned SaaS binding and transport contract before behavior is added there.
