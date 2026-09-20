# When does Lifecycle need a retention exception or legal hold beyond Runtime defaults?

## Problems solved

- Identifies product-specific retention and legal-hold obligations without asking the Agent to configure Runtime's installed default policies or cleanup worker.

## Business scenarios

- A regulator requires one contract class to be retained longer than the Runtime baseline.
- A legal matter must suspend otherwise eligible cleanup for selected subjects or resources.

## Use when

A confirmed legal, regulatory, or contractual rule changes the installed Lifecycle baseline for a named data class, resource, or subject.

## Do not use when

Runtime's installed retention policies, automatic cleanup worker, deletion replay, or ordinary aggregate deletion already satisfies the requirement. Do not ask the Agent to restate default retention or schedule cleanup.

## How to use

Record the source of the exception, affected owner and resource class, effective period, review authority, and whether it extends retention or suspends deletion. Treat the current compiler as the authoring gate: if no supported Model path can express the confirmed exception, report a Contract Gap instead of inventing Lifecycle JSON or project cleanup code.

## Adaptation cookbook

| Business requirement | Adapt with | Concrete implementation | Wrong adaptation |
| --- | --- | --- | --- |
| Tax records must be retained for ten years instead of the installed baseline | Source-owned Lifecycle retention exception | Identify the exact record class, legal basis, duration, approver, and effective version; submit it only through a published Lifecycle authoring contract when available | Replacing Runtime defaults in project code, adding `delete_after` to every record, or scheduling a custom purge job |
| Litigation suspends deletion for one customer and related contracts | Lifecycle legal hold | Scope the hold to stable subject/resource identities, record authority and review dates, and let Lifecycle block otherwise eligible cleanup | Changing retention dates manually, disabling cleanup globally, or teaching every owner a different hold rule |
| Expired ordinary records should follow standard cleanup | Runtime default Lifecycle behavior | Rely on installed policies, owner adapters, automatic worker processing, and deletion replay | Adding a product Workflow or Scheduler job to duplicate cleanup |

## Example

“Keep signed contracts for ten years because regulation X overrides the platform baseline” selects a retention exception. “Run the normal cleanup every night” does not select any product capability because Runtime owns the worker and installed policy execution.

## Permissions and scope

Publishing or releasing an exception requires the exact Lifecycle governance permission and authorized resource scope. A legal hold must fail closed when authority, scope, or review evidence is missing. Ordinary product Roles do not receive worker or system-maintenance authority.

## Boundaries

Lifecycle owns retention and hold governance; each source owner still owns its records and executes approved cleanup through registered adapters. Audit append, cleanup scheduling, retry, deletion replay, and worker leases are Runtime/module defaults and must not be rebuilt by the project.
