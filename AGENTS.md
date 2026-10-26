# Project guidance

Deskyard runs persistent Linux desktops in containers, accessed and shared by people through a browser or CLI, through a Go supervisor with a Preact web UI.

## Before making changes

- `docs/` is the project's SilverBullet space ("Container Computer"). It is synced and versioned outside this repository and ignored by git: edit its pages in place and let SilverBullet sync carry them. Never commit them, and do not link from tracked files (such as `README.md`) to paths under `docs/`, which do not exist in the repository; name the page instead.
- Read `docs/Operations manual.md` before choosing or executing work. It is the project working agreement: outcome-based planning, the owner/agent feedback loop, `$sb-inbox`, verification, handover and retention. Find it from `docs/index.md`; use `docs/AGENTS.md` for documentation formats and schemas.

- When the user invokes `$sb-inbox`, read `.agents/skills/sb-inbox/SKILL.md` and process the SilverBullet agent inbox as the explicit task.
- At startup and meaningful work checkpoints, check `sb --space "Container Computer" eval 'identity.mentions("agent")' --json`. Read the current comments and their context, handle relevant requests, and follow `docs/Operations manual.md` for replies and completion.

- Read `README.md` for the product, repository layout, configuration and development commands.
- Read `docs/AGENTS.md` and `docs/index.md` before changing code or documentation. The `docs/` folder is the project's SilverBullet documentation space; `docs/AGENTS.md` is the authoritative manual for its page formats, links, metadata and architecture diagrams.
- Find and read the architecture pages under `docs/Architecture/` and the specs and plans under `docs/superpowers/` relevant to the task. Use `docs/Architecture.md`, `docs/Specs.md` and `docs/Plans.md` as entry points; inspect the underlying Markdown pages when their SilverBullet queries are not rendered.
- Read `docs/Features.md` and `docs/Milestones.md` for capability state and agreed outcomes. Follow the record schemas in `docs/CONFIG.md` and the field contract in `docs/AGENTS.md`; keep discovery candidates distinct from approved scope.
- Check the implementation before relying on documented behavior. If code and documentation disagree, resolve the discrepancy as part of the task and distinguish implemented behavior from planned work.

## Keep documentation current

Treat the SilverBullet space in `docs/` as the shared source of project truth, including current documentation and roadmap state. Reconcile material decisions, implementation changes, feature/milestone status, blockers and verification into their authoritative pages before handover; chat summaries and comment replies do not substitute for this. Follow "Keep the truth in Markdown" in `docs/Operations manual.md`. Keep dashboard queries live; do not use baked sections. Agents verify features against agreed criteria; the owner accepts milestones. No next milestone is to be prepared or selected until requested.

Documentation maintenance is part of every task, not a follow-up. Review the documentation impact before considering work complete, and make all relevant documentation updates as part of the same task: code changes go into git, documentation edits are saved in the space.

- Before declaring any task complete, perform the architecture review in `docs/Operations manual.md` ("Keep architecture aligned with implementation"). Update affected component prose, `partOf`, `uses`, `status` and implementation paths in the same change, inspect affected generated diagrams, and report the pages updated or the concrete reason no architecture change was needed.
- Keep retained design specs accurate about current behavior and agreed future work. After implementation, extract durable decisions, evidence and remaining work, then purge the completed plan and repair its references. Remove stale specs after preserving useful rationale; the space's remote versioning keeps their history. Follow `docs/Operations manual.md` for retention details.
- Update `README.md` when user-facing behavior, setup, configuration, commands or development workflows change; keep the corresponding documentation in `docs/` consistent.
- Follow `docs/AGENTS.md` for all documentation edits. Keep that manual as the single source for documentation conventions rather than duplicating them here.
- For each task, report which documentation was updated. If no update is needed, state briefly why the existing documentation remains accurate. Do not make cosmetic edits solely to claim a documentation update.

## Implementation and verification

- `supervisor/` contains the Go supervisor and CLI; `supervisor/web/` contains the Preact/TypeScript UI. Desktop images share `images/Dockerfile`, `images/common/` and `images/tests/`; desktop-specific overlays live under `images/debian-{xfce,mate}/rootfs/`.
- Use the existing patterns in the affected component and keep changes focused on the task.
- Run checks appropriate to the change. From `supervisor/`, use `make test` for Go tests, `make typecheck` for UI type checking and tests, and `make build` to build the UI and binaries. Engine integration tests (`make test-engine`) and desktop image tests require Docker; see `README.md` for their setup.
- Validate documentation metadata and relationships with `sb --space "Container Computer" script --file scripts/check-docs.lua --json` from the repository root. This runs regression checks and asserts that whole-space SilverBullet lint has no diagnostics.
- Check documentation links, referenced paths and metadata after documentation edits. Follow the SilverBullet checks in `docs/AGENTS.md` when available; report any checks that could not be run.
- Keep `docs/Testing/Project record schemas.md` updated with observed SilverBullet/CLI failures, reproducible cases, workarounds and retest outcomes; follow the operations manual for feedback details.
- Run SilverBullet CLI (`sb`) commands outside the sandbox so they can connect to SilverBullet Desktop. A sandboxed command can misleadingly report that Desktop is not installed or has not been started; retry outside the sandbox before diagnosing Desktop availability.
- Before handing work over, review the code diff and the space pages you edited (they do not appear in `git diff`) for completeness, and report verification results and remaining limitations.
