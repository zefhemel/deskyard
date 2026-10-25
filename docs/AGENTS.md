Documentation conventions for agents (and people) adding or changing pages in this space. Read [[Operations manual|Project operations manual]] for the project workflow. `CLAUDE.md` points here; keep this the only manual for documentation conventions.

# The space
- The space root is `docs/` in the Deskyard repository. It's registered in SilverBullet Desktop as **Container Computer**: use `sb --space "Container Computer" …` for queries and checks. Run these commands outside the sandbox so they can reach Desktop.
- Pages are plain Markdown and must stay readable on GitHub: essential facts belong in prose; queries provide overviews. Maintain substantive content and roadmap state under [[Operations manual#Keep the truth in Markdown|Keep the truth in Markdown]]; metadata validity and live query output do not establish factual accuracy.
- [[index]] is the entry page. [[CONFIG]] contains the executable tag schemas, validators and display helpers. `Library/` contains installed libraries, including [[Library/silverbullet-diagram/Mermaid]]; don't edit them, because the Library Manager updates them. `screenshot.png` is the repository README's desktop screenshot.

# Record types
Use one project record tag per page. A tag describes what a record is; status, result and relationships are fields, not additional tags. The page path is its identity; no separate numeric identifier is required. Tasks use SilverBullet's built-in task objects and stay in implementation plans rather than becoming another custom page type.

| Tag | Purpose and location | Required fields | Allowed states/results |
| --- | --- | --- | --- |
| `feature` | Durable capability, improvement or defect in `Features/` | `displayName`, `status`, `kind`, `outcome`, `acceptance` | `idea`, `discovery`, `ready`, `building`, `implemented`, `delivered`, `parked`, `cancelled` |
| `milestone` | Bounded outcome in `Milestones/` | `displayName`, `status`, `outcome`, `acceptance`, `features` | `proposed`, `active`, `achieved`, `cancelled` |
| `spec` | Agreed design in `superpowers/specs/` | `displayName`, `status` | `draft`, `approved`, `implemented`, `superseded`, `cancelled` |
| `plan` | Implementation steps in `superpowers/plans/` | `displayName`, `status` | `draft`, `active`, `done`, `cancelled`, `superseded` |
| `component` | Current or explicitly planned architecture in `Architecture/` | `status` | `built`, `planned`, `external` |
| `spike` | Feasibility investigation in `superpowers/spikes/` | `displayName`, `status`; `conclusion` when concluded | `active`, `concluded`, `superseded` |
| `verification` | Scoped evidence in `Testing/` | `displayName`, `result`, `scope` | `passed`, `partial`, `failed`, `inconclusive` in `result` |
| `meta` | Space guidance and configuration | No additional required fields | No lifecycle |

`implemented` means the capability exists in the inspected code; it does not retrospectively certify every acceptance criterion or environment. `delivered` means agents verified the agreed feature acceptance and recorded evidence. The owner accepts milestones rather than every feature. Feature `availability` is independently `available`, `experimental` or `unavailable`; `summary` describes current behavior, optional `limitations` is a nonempty string list, and `next` records a concrete next action for open discovery or implementation work.

Optional `summary`, `next` and `attention` fields are supported on records. `attention` is an object with exactly four required fields: `kind` (`decision`, `review`, `input`, `acceptance`), nonempty `request`, nonempty `recommendation`, and boolean `blocking`. It is the source of the owner's attention queue; remove it when resolved after recording the durable decision. Requests to agents use comments instead. Do not add an attention request merely to fill the dashboard.

Dashboard queries and widgets remain live. Do not use baked sections. Essential facts, limitations and next actions live in the source Markdown records. [[Dashboard views]] derives the home, capability and loose-end tables; [[Operations manual#Check project status and owner attention|The working agreement]] defines how to maintain them.

`kind` on a feature is `capability`, `improvement` or `defect`. `outcome` is a nonempty sentence; `acceptance` is a nonempty list of observable criteria. A feature is `ready` only when the proposed scope and acceptance are agreed. `delivered` and `achieved` require links in `evidence` to verification records; humans must still assess that the evidence satisfies the stated acceptance. A link to partial evidence does not by itself establish acceptance.

Milestones have no planning dates or deadlines. Features have no planning dates either. Retained specs, plans, spikes and verification records may have an optional `date` string in `YYYY-MM-DD` form; this records authorship or observation, never a delivery promise. Retained dated Superpowers paths stay stable; the retention rules in [[Operations manual]] govern removal. New feature and milestone paths use meaningful names without dates.

`passed` means the declared verification scope passed in the recorded environment; `partial` means coverage or checks are incomplete; `failed` means the scoped check failed; `inconclusive` means the observations cannot settle it. Preserve environment details, failures, workarounds and coverage limitations in the body. Do not use `status: complete` for evidence. Concluding a spike means its findings are recorded, not that its subject is implemented.

Product direction, [[Use cases]], licensing, indexes and removed architecture identities can remain ordinary pages. Do not use `meta` as a catch-all for product content. Decisions belong in the relevant spec or feature until separate decision records have a concrete need.

# Fields and relationships
All relationship values are whole-page wiki links, rooted at the space root. Single links are strings; plural fields are nonempty lists. Omit optional fields when unused; don't write YAML null or empty lists. Schemas validate link syntax; native tag validators check that targets exist and carry the right record tag. Self-relations are rejected.

| Source type | Optional relationships and target types |
| --- | --- |
| Feature | `components`: component list; `dependsOn`: feature list; `evidence`: verification list |
| Milestone | `features`: feature list (required scope); `evidence`: verification list |
| Spec | `plan`: plan; `features`: feature list; `supersedes`: spec list |
| Plan | `spec`: spec; `features`: feature list; `milestone`: milestone |
| Component | `partOf`: component; `uses`: component list; `spec`: spec; `features`: feature list |
| Spike | `features`: feature list; `spec`: spec |
| Verification | `features`: feature list; `spec`: spec; `plan`: plan; `milestone`: milestone; `components`: component list |

`scope`, `conclusion`, `environment`, `outcome` and `displayName` are nonempty strings when supplied. `environment` is optional on verification records. Link labels can use the target's display name. In `partOf` and `uses`, keep links unlabelled so architecture diagram labels come from page names. A component page's final path segment is its box label.

Maintain links on the referring record and query reverse relationships instead of duplicating history lists. The existing spec/plan pair links remain supported. Specs and plans can concern several features. A milestone's feature list defines scope; its body specifies the slice and acceptance. A plan normally targets one milestone. Split future implementation plans when outcomes need separate acceptance rather than giving one plan an ambiguous set of milestone statuses. Historical prototypes may have no spec; do not fabricate retrospective approval or associations merely to satisfy a schema.

Example feature metadata (the linked evidence is only needed when delivered):
```yaml
---
displayName: Custom DNS
tags: feature
status: discovery
kind: capability
outcome: Administrators can select DNS independently of VPN.
acceptance:
  - Resolver selection works with and without VPN.
---
```

Example milestone metadata (illustrative, not a priority commitment):
```yaml
---
displayName: Independent resolver policy
tags: milestone
status: proposed
outcome: A desktop can use an explicitly selected resolver.
features:
  - "[[Features/Custom DNS]]"
acceptance:
  - The selected resolver is used with VPN enabled and disabled.
  - Resolver outage behavior matches the approved design.
---
```

Specs and plans are written with the Superpowers brainstorming and writing-plans skills and retain their dated paths. Add the appropriate frontmatter before handing them over, replacing redundant `Date:`/`Status:` prose and the `#` title, which `displayName` supplies. New plans start `draft` until approved for execution. Tick completed steps; explicitly record cancelled, superseded or deferred steps rather than treating cancellation as successful completion. Historical unchecked tasks are not proof of remaining work: inspect amendments, code and evidence before changing their status.

# Frontmatter previews
Project record previews share a labelled, bordered metadata panel, visually separate from the document body. Show populated record fields, including outcome, acceptance, limits, next actions, owner attention (with blocking state), verification scope/environment and typed relationship links. Component previews use the same panel and retain their clickable generated neighbourhood diagram. Use the preview’s ⋯ → Edit source action or Alt-click to edit the underlying YAML. The renderer and theme-aware Space Style live in [[CONFIG]]; no baked content is written.

# Comments and agent inbox
The project workflow for comments, replies, completion and `$sb-inbox` is maintained in [[Operations manual#Comments and agent inbox|Comments and agent inbox]].

# Retention and current truth
The project retention policy is maintained in [[Operations manual#Retention and current truth|Retention and current truth]]. Follow it before purging completed plans, stale specs or evidence.

# Architecture maintenance
Follow [[Operations manual#Keep architecture aligned with implementation|The architecture completion review]] for every task. The metadata contract lives here: one component page per meaningful responsibility; its final path segment supplies the diagram label. Keep current responsibilities, interfaces and existing implementation paths in prose.

[[Architecture]] draws all components with `docs.architecture()`, and each component card shows its neighbourhood. `partOf` is a single unlabelled component link defining containment; `uses` is a list of unlabelled component links defining dependency arrows. Keep both accurate when components move or interact differently; never draw architecture diagrams by hand. Use `built` for implemented repository components, `planned` for approved unimplemented components with a `spec` link, and `external` for dependencies outside this repository. A link to a discovery feature does not make its proposed behavior implemented.

When a component is removed, remove its tag and obsolete relationships, repair references and follow [[Operations manual#Retention and current truth|Retention rules]] for deleting its page. When metadata or diagram rendering changes, reindex and inspect the full graph and affected neighbourhoods as described below.

# Writing conventions
- A page starts with a short summary and uses `#` for its sections. Use one line per paragraph, without hard wrapping, and `-` for bullets.
- Link pages with rooted wiki links, preferably labelled with their display name: `[[superpowers/specs/2026-10-21-supervisor-design|Supervisor design]]`. Renaming a file requires updating links.
- Images and other page files go in a folder named after the page and use relative Markdown links, so they work in SilverBullet and on GitHub.
- Keep the eight project record tags above stable. Statuses, priorities, teams and platforms are attributes, not new record types. Register a new record type in [[CONFIG]] and document its field contract here before using it.

# Checking changes
Use SilverBullet's actual lint infrastructure, not a separate approximation of its schemas. `tag.define` schemas and validators run in the editor's object linter and in `space.lint()`. Invalid records remain indexed so mistakes don't hide work from dashboards. Immediate children of the typed folders must carry their corresponding tag; supporting files in nested prototype folders are exempt. A small `editor:lint` listener reports missing frontmatter, which otherwise lacks an object range for a native diagnostic. Listener callbacks receive the payload in `event.data` and must return `nil` when there are no diagnostics: an empty Lua table becomes a JavaScript object and is not a diagnostic array.

After changing configuration, reload the runtime. Reindex after changes to indexing transforms, then check counts and rendered pages:
```sh
sb --space "Container Computer" eval 'editor.invokeCommand("Space: Reindex")'
sb --space "Container Computer" eval 'system.reboot()'
sb --space "Container Computer" script --file scripts/check-docs.lua --json
sb --space "Container Computer" eval 'space.lint()' --json
sb --space "Container Computer" query 'from p = index.contentPages "feature" select {page=p.name,status=p.status}' --json
sb --space "Container Computer" eval 'editor.navigate("Features")'
sb --space "Container Computer" screenshot /tmp/deskyard-features.png
```
Run shell commands from the repository root. The regression script uses the live native object validator, tests invalid metadata and typed relations, dispatches actual editor lint events (including the clean-page response contract), and asserts a clean whole-space lint result. A successful `sb eval` exit does not mean there are no lint diagnostics: inspect the returned list, or use the regression script's assertion.

Check edited Markdown links and named source paths too. Schemas do not prove code behavior, truthful completion claims, dependency acyclicity, reciprocal spec/plan links or task completion; verify those against implementation and evidence. Existing historical status/task reconciliation is separate from metadata validity. Other SilverBullet metadata remains allowed, so these schemas are not an unknown-field spell checker. Report which documentation changed, the checks run and any limits before handing work over.
