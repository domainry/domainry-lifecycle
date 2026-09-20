# domainry-lifecycle

Product Agent question index: [`capability/agent/index.json`](capability/agent/index.json). Installed retention policies, cleanup workers, and deletion replay are Runtime defaults and are intentionally absent; product routing exposes only confirmed retention/legal-hold exceptions and subject-right requests.

Reusable, in-process data-lifecycle governance for Domainry hosts.

Lifecycle owns its domain policy, application use cases, `_lifecycle_*` tables,
module HTTP product adapter, and local maintenance tick. It borrows the host
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

`AccountErasureBinding.AccountErasures()` provides trusted Action delivery.
Runtime resolves the published profile binding, account, organization and
approval before staging a request. Staging requires the host Action transaction
and an independently approved request from the subject. The queue and approval
provenance roll back with a failed business Action. An approved queue receipt
does not mean the account has been erased.

The account-erasure worker executes only committed Action approvals through the
existing subject executor. Failed cleanup retains its saved plans and waits
before retrying. Receipt reads match the exact workspace, organization, binding,
object and profile. Irreversible owner/file operations cannot run inside the
staging Action transaction.

Lifecycle also participates as an erasure owner: it fences new subject exports,
removes previous export files and execution payloads, and redacts prior subject
request and audit payloads. Executing exports block preparation. File references
are frozen before redaction so cleanup can recover after a file-store failure.

## HTTP ownership

The embedded module contributes 17 authenticated management/operations routes
through `modulehttp.Adapter`, including policy, legal-hold, cleanup creation and
preview, metrics, archive evidence, subject requests, external-erasure
reconciliation, and deletion replay. Lifecycle owns their request DTOs,
redaction, permissions, governance metadata, and OpenAPI operations.

Every role-facing route rechecks its exact Action permission and compiles that
same grant's Identity-owned `data_scope` (`all`, `owner`, `org`, `org_child`, or
`target_org`) into explicit repository predicates. Workspace and scope filters
are applied by `domainry-orm` while querying; scoped mutations pre-read and
write under the same host transaction and repeat the filter in the final DML.
`all` adds no data-range predicate. Lifecycle stores only natural business
ownership facts on policy versions, legal holds, cleanup jobs, and subject
requests; archive, external-erasure, and deletion-registry access follows their
job/request relationship. Worker leases, fencing tokens, execution steps,
artifact cleanup, and other internal maintenance state remain system-scoped.

The host still owns authentication/listener mounting and common transport
governance. A Runtime host may additionally own orchestration endpoints that
span Runtime infrastructure. In domainry-runtime, only
`POST /lifecycle/cleanup/jobs/{jobID}/run` stays Runtime-owned because
it creates/replays a durable Operations receipt before invoking Lifecycle.

## Source layout

- `internal/domain/lifecycle` owns models, repository ports, invariants, state
  transitions, eligibility, and default-policy construction.
- `internal/application/lifecycle` coordinates Lifecycle use cases, audit
  evidence, transactions, workers, and crash recovery.
- `internal/adapter/lifecyclesdk` converts between internal values and the
  stable public SDK contracts.
- `internal/transport/http/module` owns the embedded product HTTP adapter and
  OpenAPI operations.
- `internal/infrastructure/persistence` owns ORM-backed storage, artifacts,
  schema, and host-registered migrations.
- `internal/assembly/module` and public `module` form the embedded composition
  root and its thin implementation entrypoint.

`internal/assembly/saas`, `internal/transport/http/saas`, and
`cmd/lifecycle-server` remain empty boundaries. A standalone deployment needs a
versioned SaaS binding and transport contract before behavior is added there.
