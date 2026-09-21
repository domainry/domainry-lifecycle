# How should subject export and erasure be coordinated?

## Problems solved

- Gives one governed process for subject export and erasure while each source owner remains responsible for its own data and legal exceptions.

## Business scenarios

- Fulfilling a subject access request across project records, audit evidence, and external CRM mappings.
- Closing a customer account by erasing eligible data while retaining legally held evidence and reporting per-owner completion.
- Retrying only a temporarily failed owner while completed owners keep their durable idempotent results.

## Use when

Use subject-right coordination when a verified person’s export/erasure request spans Identity, project records, Audit rules, or external providers.

## Do not use when

Do not use it for an ordinary user-initiated delete of one business record.

## How to use

Resolve the subject through Identity, collect source-owner handlers, check legal holds, independently approve when required, execute idempotently, and preserve result evidence.

## Adaptation cookbook

| Business requirement | Adapt with | Concrete implementation | Wrong adaptation |
| --- | --- | --- | --- |
| A verified person requests all data held about them | Lifecycle export request plus owner adapters and Data Exchange artifact | Resolve subject identifiers, ask each registered owner for an authorized bounded contribution, assemble a durable export, and record per-owner completion | Querying every database table from Lifecycle or returning an unbounded live response |
| Account closure requires erasure except legally retained evidence | Lifecycle erasure request with legal exception outcomes | Each owner erases or anonymizes eligible data and reports `completed`, `retained` with basis, or `failed`; Lifecycle aggregates the final result | Claiming full erasure while silently leaving held records or deleting Audit evidence contrary to policy |
| External CRM also holds mapped subject data | Lifecycle obligation routed through Integration | The CRM-owning adapter resolves the provider identity, invokes an idempotent external erase/export, and stores the provider receipt | Giving Lifecycle raw provider credentials or bypassing Integration reconciliation |
| One owner is temporarily unavailable | Retryable owner task with partial status | Keep completed owner results durable, retry only the failed owner, and expose that the overall request is not yet complete | Restarting the whole request and duplicating already-completed external effects |

## Example

For a verified subject export, Lifecycle asks Identity, project Objects, Notification, Audit, and mapped external owners for bounded contributions; every owner reports `completed`, `no_data`, `retained/restricted`, or `failed`, and Data Exchange assembles the authorized Artifact. For erasure, a CRM owner may complete while Audit reports Legal Hold and Notification is temporarily unavailable; the overall request remains recoverable and does not claim full completion. Retrying preserves the original request identity and skips completed owners. Deleting one ordinary order is not a subject-right request.

## Permissions and scope

Request submission, identity verification, approval, execution, and evidence read are separate. No caller may name an arbitrary subject outside their authority.

## Boundaries

Identity owns subject resolution; Integration owns external deletion calls; each source owner owns its data semantics.
