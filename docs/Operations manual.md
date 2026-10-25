---
displayName: Project operations manual
tags: meta
---
How the owner and agents run the Deskyard project: choose outcomes, develop and verify changes, exchange feedback, and keep the working documentation accurate. Start here for ways of working. [[AGENTS]] remains the single source for documentation formats and field conventions; [[CONFIG]] contains their executable schemas. The repository's `AGENTS.md` directs new agents here.

# Keep the truth in Markdown
The repository's Markdown is the shared, durable source of project truth: current behavior, design rationale, agreed future work, roadmap state and open decisions. Keep it up to date as work happens. Chats, comments, agent memory and private task lists are inputs to that record; SilverBullet queries, cards and diagrams are views derived from it.

Before handing work over or closing a request, reconcile every material outcome into the relevant pages. Update current behavior and interfaces in architecture and retained specs; update feature state, milestone scope, dependencies, blockers and acceptance in roadmap records; and retain verification results and limits in evidence records. Record approved decisions as approved, proposals as proposals and unresolved questions as unresolved. Do not turn an idea mentioned in a conversation into an agreed commitment.

Resolve conflicts against the implementation and observed behavior for claims about what exists, and against the owner's explicit decisions for what is agreed. Repair the Markdown as part of the same work. A comment reply or chat summary does not complete that reconciliation, and a clean schema check does not establish factual accuracy. Essential facts must remain understandable by reading the source pages without a running SilverBullet instance.

Each fact has one authoritative home, linked from other records and derived views. Dashboard queries and widgets remain live; do not use baked sections. Keep essential facts in the underlying Markdown records so live rendering is not the sole record of project state. Apply the retention rules to remove execution scaffolding and obsolete descriptions after carrying forward what remains useful. The target is a coherent current record, with Git preserving history.

# Get oriented

1. Read the repository `README.md` for the product, current behavior and development commands, then this manual and [[AGENTS]].
2. Open [[index]], [[Milestones]] and [[Features]] to identify agreed outcomes and current capability state. The family use case is the immediate direction in [[Use cases]]; discovery candidates are not approved implementation scope.
3. Check [[Agent inbox]] for owner feedback, then read the relevant [[Architecture]] components, retained [[Specs]], and active [[Plans]]. Inspect actual Markdown pages when query results are unavailable.
4. Check the working-tree diff and implementation before changing files. Preserve work already in progress and distinguish current behavior, agreed future work and experimental findings.

# Work from outcomes to evidence

| Record | Operational role |
| --- | --- |
| [[Use cases]] | Product needs and open questions; confirm assumptions before committing scope |
| [[Features]] | Durable capabilities, improvements and defects, including current state and acceptance criteria |
| [[Milestones]] | Bounded iterations defined by an outcome and acceptance, without planning dates |
| [[Specs]] | Current design contracts, constraints and useful rationale, plus explicitly agreed future designs |
| [[Plans]] | Temporary implementation instructions and task checkboxes for active work |
| [[Architecture]] | Current responsibilities, interfaces, dependencies and implementation paths |
| [[Spikes]] | Feasibility findings with explicit conclusions and limits |
| [[Verification]] | Scoped evidence, environments, results and remaining limitations |

Choose a bounded feature slice within the agreed milestone or explicit user request. Record its outcome, acceptance criteria and dependencies. Use the Superpowers brainstorming skill for design and writing-plans for implementation planning when applicable; follow existing approvals rather than restarting agreed discussions. Link new specs and plans to their features and plans to their milestone where relevant. Use the tag schemas and states in [[AGENTS#Record types|Record types]]. Discovery records do not establish approved milestone commitments or a complete capability inventory. Read the live feature and milestone records for current priorities; this manual defines the workflow.

Implement against the agreed scope, verify behavior with checks appropriate to the affected component, and update feature state, design and architecture in the same change. Store material evidence and limitations in verification records. Do not infer product readiness from task-checkbox counts or treat partial coverage as general certification. Agents verify feature delivery against agreed acceptance criteria and record scoped evidence. The owner accepts milestone outcomes; individual features do not require a separate owner acceptance step unless the user specifically asks for one. Existing capability inventory records use implemented to distinguish present code from freshly verified delivery.

Before handover, review implementation and documentation together, report checks and limitations, and identify any decision needed from the owner. Apply the retention rules below to completed plans and stale designs.

# Check project status and owner attention
[[index]] is the owner dashboard. It shows explicit requests for the owner, active milestone scope, actual implementation work, current capabilities, unscheduled loose ends and documentation-health follow-ups. The source fields and native schemas are documented in [[AGENTS]]; [[Dashboard views]] defines the live derived views. No baked sections are used.

Owner requests live in the relevant record's `attention` field. State the exact question, recommendation and whether progress is blocked; use decision, review, input or acceptance as the request kind. Remove the field when resolved and carry the decision into the authoritative content. Do not fabricate urgency or interpret every partial historical test as a current blocker. An empty attention section means no explicit owner request is recorded, not that every deployment scenario is certified.

[[Features]] distinguishes code that is implemented from a feature whose agreed acceptance has been verified. Its independent availability field distinguishes available interfaces, experimental support and unavailable desired capabilities. [[Loose ends]] records concrete unscheduled next actions and parked investigations. The inventory is source-backed at the documented capability granularity, with environment coverage and limits in [[Testing/Current capability inventory]].

The owner deferred preparation and selection of a next milestone. Do not create proposed milestones or a priority order merely to fill a dashboard. When an outcome is selected later, define its scope and acceptance without planning dates. Agents verify features; the owner accepts the milestone.

Requests **to agents** remain in [[Agent inbox]] and are separate from decisions and reviews **requiring the owner**. Read the current records rather than treating a cached index or a status count as proof of behavior. An implementation-plan checkbox is not product acceptance.

# Comments and agent inbox
Use SilverBullet comments with the `agent` at-mention to direct questions, corrections or requests to coding agents beside the relevant content, on any page in the space. The shared identity is registered in [[CONFIG]] for autocomplete. [[Agent inbox]] and the native Mention Inbox collect these requests; they are work input, not another project record type.

```markdown
<!--
@agent Please check whether this still describes the current implementation.
-->
```

Invoke the repository skill with `$sb-inbox` to explicitly process pending feedback now. Its entrypoint is `.agents/skills/sb-inbox/SKILL.md`; it is explicit-only and delegates comment-handling rules to this manual. In Codex CLI, `/skills` also opens the skill selector.

At the start of work, before choosing a new task, and at meaningful checkpoints during ongoing work, query the entire inbox:
```sh
sb --space "Container Computer" eval 'identity.mentions("agent")' --json
```
Read the current page and surrounding comment before acting; indexed snippets and positional references can become stale after edits. If pagination is used, consume all pages while `truncated` is true. Check newly arrived comments for relevance to the active work; record unrelated requests for subsequent work rather than silently replacing the current task. Comments guide the work within the user's existing scope and approval boundaries.

For a straightforward request, make the change and append a concise `Agent:` response in the same comment explaining the outcome and verification. For questions, discussion or a dependency on user input, reply in the comment and keep the addressing mention open. Do not use the addressing token in the agent's own response, which would create another request. Never claim a mention is complete merely because it has been read.

After the request is satisfied, remove just the addressing mention using the native Mention Inbox's **Remove mention** action or its `index.resolveAtMention` API, retaining the original comment and response for the user's review. Re-read before any edit and use fresh ranges. For a task request, completing its checkbox also removes it from the native open mention list. SilverBullet's comment **Resolve** action deletes the entire comment; leave that to the author unless explicitly asked to resolve it. Carry durable decisions into the document body before their comments are discarded.

This is a persistent queue checked while an agent is working. Adding a mention alone does not establish a background monitor; unattended polling or automatic chat wake-up requires an explicitly configured automation. On the owner dashboard, keep requests **to the agent** separate from decisions and reviews **requiring the owner**.

# Retention and current truth
The working space documents the current system and agreed future work. Git preserves past implementations and discarded approaches; keeping a Markdown page is not required merely to retain history.

- Implementation plans are temporary execution aids. After verifying completion, move durable decisions into the current spec or component pages, move evidence into verification records, and move genuinely unfinished work into feature records or a new active plan. Then remove the completed plan. Cancelled and superseded plans can be removed once their remaining useful information has been handled in the same way.
- Before removing a plan, remove its `plan` metadata from specs and verification records and replace prose links with the current spec, feature or evidence. Preserve the referenced evidence files before deleting any plan attachment folder. Do not turn old unchecked checkboxes into a fresh backlog without checking the code and later decisions.
- Retain a design spec while it explains the current design, a still-relevant constraint or tradeoff, or agreed future work. Keep its main narrative accurate; remove obsolete instructions and implementation-path descriptions after preserving any rationale that still matters. A pile of dated amendments is a signal to consolidate the current design.
- Remove stale specs for abandoned, removed or superseded designs after moving any useful rationale into the current records and repairing incoming references. A superseded status is a transition during cleanup, not a permanent archive category. A replacement spec is not required when the feature itself was removed.
- Verification and spike records may retain older observations when they still explain a current limitation, regression or decision. Label the environment and scope clearly and link the current record; purge them when they have no remaining value. Historical evidence is not the authority for current behavior.
- Removed component identities need no permanent tombstone. Replace their useful references with current components or a concise explanation in a retained record, then remove the obsolete pages.

Do not purge uncommitted work or evidence merely because a record's status says done. Inspect the diff, implementation, current references and any work in progress first. If an immutable historical reference remains necessary, link a verified Git revision rather than leaving a misleading page in the working space. Current architecture and feature state are the agent's starting point; read design specs for current contracts and rationale, and active plans for execution steps.

# Keep architecture aligned with implementation
Architecture review is part of every task's completion check. Update affected pages in the same change as the implementation, rather than leaving documentation work for later. If a task has no architectural effect, say why at handover; do not make cosmetic page edits just to claim an update.

1. **Establish the baseline.** Read the affected component pages and inspect the actual implementation before designing the change. Follow calls and interfaces across component boundaries; repair discrepancies encountered within the task's scope instead of copying stale assumptions into a new spec.
2. **Compare the final implementation.** Review the diff for added, removed or moved responsibilities; dependencies; API, CLI and event contracts; persistence and recovery; startup/shutdown; configuration; and externally visible failure behavior. A bug fix can change these contracts even when it adds no component.
3. **Update affected component pages.** Explain what the component does now, how callers reach it, what it owns, its important limits and which existing files implement it. Update neighbouring pages when a relationship changes. Use [[AGENTS#Architecture maintenance|Architecture metadata conventions]] for `partOf`, `uses` and `status`; these fields generate the diagrams.
4. **Separate current and future behavior.** Mark approved, unimplemented components `planned` and link their spec. Switch to `built` only when implemented. Keep prototypes and unapproved possibilities in features or spikes. Rewrite obsolete present-tense descriptions; a dated amendment does not make contradictory current documentation accurate. Retain useful rationale through the retention policy.
5. **Handle removals and moves.** Remove obsolete dependencies, repair incoming links and update named implementation paths. Remove component tags from parts that no longer exist; prune obsolete pages when their remaining useful information has been retained. Do not leave removed functionality visible as a built component.
6. **Verify the result.** Run native SilverBullet lint, check edited links and referenced source paths, and, when relationships or component status change, inspect both the full [[Architecture]] graph and affected component neighbourhoods. Generate diagrams from metadata rather than drawing a competing graph. Lint validates structure and links; compare the prose and relationships to code to establish accuracy.
7. **Report the review.** Name the architecture pages updated and relevant verification. If none changed, give the concrete reason, such as a copy-only change with no responsibility, interface, dependency or lifecycle change. State any unavailable rendering or runtime checks rather than implying they passed.

Do not declare implementation complete until this review is done. Keep the detailed metadata and page-format rules in [[AGENTS]]; this section defines when and how the review fits into delivery.

# Verification and handover
Use the existing checks described in the repository `README.md` and root `AGENTS.md`. Run SilverBullet CLI commands outside the sandbox so they can connect to Desktop. The documentation gate, run from the repository root, is:

```sh
sb --space "Container Computer" script --file scripts/check-docs.lua --json
```

This asserts passing schema regression checks and a clean whole-space `space.lint()` result. Inspect lint output rather than interpreting a successful evaluation exit as proof that there were no diagnostics. Follow [[AGENTS#Checking changes|Checking changes]] for configuration refresh, rendered-page checks and link validation.

For changes involving SilverBullet itself, also check the interactive editor: native lint alone previously missed a malformed event result that crashed rendering. Keep [[Testing/Project record schemas|Project record schema verification and SilverBullet feedback]] updated as this workflow exercises SilverBullet and its CLI. Record observed failures and surprising behavior with the runtime/version, minimal reproduction, expected versus actual result, scope, workaround or fix, and remaining questions. Distinguish confirmed observations from inferred causes and record retest outcomes when an issue changes; do not manufacture issues merely to update the page. Use additional scoped verification records when their evidence warrants it, linking them from that feedback page. Keep a project configuration workaround distinct from an upstream SilverBullet or CLI fix. A failed CLI call is not an empty inbox or a passing check.

A handover states what changed, which documentation was updated, what was actually verified and what still requires input. Preserve current instructions in this manual and documentation conventions in [[AGENTS]] rather than letting important operating rules exist only in a chat or skill.
