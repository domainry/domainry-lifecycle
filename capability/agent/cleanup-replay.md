# How should governed cleanup and deletion replay recover without repeating completed effects?

## Problems solved

- Previews and executes installed retention policy as durable, auditable work, and replays only registered deletion obligations that remain after restore or partial failure.

## Business scenarios

- An operator previews expired artifacts for one retention policy before creating an approved cleanup job.
- A restored backup still contains registered deletion obligations that must be replayed without repeating already completed owner effects.

## Use when

Use cleanup for operational execution of an installed Lifecycle policy. Use deletion replay when a restore or interrupted owner deletion leaves durable registrations that have not reached their required terminal effect.

## Do not use when

Do not use cleanup for an ordinary business-record delete, to invent a new retention rule, or to satisfy a verified subject request. Do not use deletion replay as a generic retry button for an external effect whose outcome is unknown.

## How to use

Preview one exact policy first, review its owner-produced impact, and create a cleanup job only through the high-risk governed Action. Runtime workers claim and progress that durable job; project code does not schedule or process it. After a restore, replay reads registered deletion obligations, stops on active legal hold, and records each successful replay with the original request identity.

## Adaptation cookbook

| Business requirement | Adapt with | Concrete implementation | Wrong adaptation |
| --- | --- | --- | --- |
| Review what the installed retention policy would remove | Cleanup preview | Query the exact `policy_key`; inspect owner counts and evidence without creating a job or deleting data | Running a delete query in the application or treating a record count as approval |
| Execute an approved retention cleanup | Durable cleanup job | Create one audited job, let the Runtime worker claim it with a lease, and inspect progress and terminal evidence | Creating a Scheduler definition or looping over owner tables in a Handler |
| Restore reintroduces data covered by durable deletion registrations | Deletion replay | Replay the bounded registered obligations, preserve the original request identity, and append `lifecycle.deletion.replayed` evidence for each completed registration | Re-running every historical erasure request or scanning every table for likely deleted subjects |
| One replayed owner effect is uncertain | Operator recovery, not blind replay | Stop, inspect owner/provider evidence, reconcile the uncertain effect, and resume only obligations proven incomplete | Repeating the external delete until it appears successful and risking duplicate irreversible effects |

## Example

An operator first previews policy `expired_subject_exports`, receives twelve eligible artifacts, and creates one cleanup job with the required approval reason. The worker durably claims and processes that job. Months later a database restore exposes two registered deletion obligations; replay handles only those registrations, refuses work covered by an active legal hold, and keeps the original request and audit correlation.

## Permissions and scope

Preview is a governed read. Creating cleanup work and replaying deletion are separate high-risk write permissions with caller idempotency and mutation audit. The caller cannot select arbitrary Workspace data outside the Principal scope, and the worker uses system authority only for already-authorized durable work.

## Boundaries

Lifecycle owns policy execution, cleanup job state, deletion registrations, and replay evidence. Source owners execute their own deletion semantics. Audit preserves governance evidence. Cleanup and replay are operational capabilities, not `backend/model` authoring collections, and they never replace subject-right verification or ordinary aggregate deletion.
