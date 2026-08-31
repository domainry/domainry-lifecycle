# lifecycle-server

This is the reserved standalone process boundary for Lifecycle.

No executable is published yet because `domainry-lifecycle-sdk` v0.1.5 defines
only the embedded `OpenModule` contract. A server must not invent a second
database owner, migration ledger, transaction model, or unversioned HTTP API.
Once the SDK defines a SaaS binding and transport contract, the executable in
this directory will compose `internal/assembly/saas`.
