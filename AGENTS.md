# Project guidance

Deskyard runs persistent Linux desktops in containers, shared by people and AI agents, through a Go supervisor with a Preact web UI.

## Before making changes

- Read `README.md` for the product, repository layout, configuration and development commands.
- Read `docs/AGENTS.md` and `docs/index.md` before changing code or documentation. The `docs/` folder is the project's SilverBullet documentation space; `docs/AGENTS.md` is the authoritative manual for its page formats, links, metadata and architecture diagrams.
- Find and read the architecture pages under `docs/Architecture/` and the specs and plans under `docs/superpowers/` relevant to the task. Use `docs/Architecture.md`, `docs/Specs.md` and `docs/Plans.md` as entry points; inspect the underlying Markdown pages when their SilverBullet queries are not rendered.
- Check the implementation before relying on documented behavior. If code and documentation disagree, resolve the discrepancy as part of the task and distinguish implemented behavior from planned work.

## Keep documentation current

Documentation maintenance is part of every task, not a follow-up. Review the documentation impact before considering work complete, and make all relevant documentation updates in the same change.

- Update `docs/Architecture/` when responsibilities, dependencies, interfaces, implementation paths or component status change. Keep `partOf`, `uses` and `status` accurate so the generated diagrams stay correct.
- Keep affected specs and plans aligned with the work: mark completed tasks, update statuses and record material design changes. Preserve historical context and clearly identify superseded decisions.
- Update `README.md` when user-facing behavior, setup, configuration, commands or development workflows change; keep the corresponding documentation in `docs/` consistent.
- Follow `docs/AGENTS.md` for all documentation edits. Keep that manual as the single source for documentation conventions rather than duplicating them here.
- For each task, report which documentation was updated. If no update is needed, state briefly why the existing documentation remains accurate. Do not make cosmetic edits solely to claim a documentation update.

## Implementation and verification

- `supervisor/` contains the Go supervisor and CLI; `supervisor/web/` contains the Preact/TypeScript UI. Desktop images share `images/Dockerfile`, `images/common/` and `images/tests/`; desktop-specific overlays live under `images/debian-{xfce,kde,mate}/rootfs/`.
- Use the existing patterns in the affected component and keep changes focused on the task.
- Run checks appropriate to the change. From `supervisor/`, use `make test` for Go tests, `make typecheck` for UI type checking and tests, and `make build` to build the UI, scanner and binaries. Engine integration tests (`make test-engine`) and desktop image tests require Docker; see `README.md` for their setup.
- Check documentation links, referenced paths and metadata after documentation edits. Follow the SilverBullet checks in `docs/AGENTS.md` when available; report any checks that could not be run.
- Run SilverBullet CLI (`sb`) commands outside the sandbox so they can connect to SilverBullet Desktop. A sandboxed command can misleadingly report that Desktop is not installed or has not been started; retry outside the sandbox before diagnosing Desktop availability.
- Before handing work over, review the diff for both implementation and documentation completeness, and report verification results and remaining limitations.
