---
tags: meta
---
Operating manual for agents (and people) adding or changing pages in this space: the design documents and architecture of Deskyard. `CLAUDE.md` points here; keep this the only manual.

# The space
- The space root is `docs/` in the Deskyard repository. It's registered in SilverBullet Desktop as **Container Computer**: use `sb --space "Container Computer" …` for queries and checks.
- Pages are plain Markdown and must stay readable on GitHub: put essential facts in prose, and use queries only for overviews.
- [[index]] is the entry page. [[CONFIG]] holds the space's Lua configuration. `Library/` holds installed libraries (the Diagrams library, [[Library/silverbullet-diagram/Mermaid]]); don't edit them, they're updated by the Library Manager. `screenshot.png` is the desktop screenshot the repository README shows.

# Kinds of pages
Specs and plans are written with the superpowers skills (brainstorming, writing-plans) and stay where those skills put them, under their dated file names. What makes them SilverBullet pages is their frontmatter: the tag is what [[Specs]], [[Plans]] and [[index]] query, and `displayName` is how they're shown in links, the page picker and those lists.

**Spec** (`superpowers/specs/YYYY-MM-DD-<topic>-design.md`): what we build and why.
```yaml
---
displayName: Supervisor design
tags: spec
status: draft            # draft, approved, implemented, superseded
date: "2026-10-21"       # when the spec was written
plan: "[[superpowers/plans/2026-10-21-topic|Topic plan]]"   # once a plan exists
---
```

**Plan** (`superpowers/plans/YYYY-MM-DD-<topic>.md`): how a spec gets built, as steps written as tasks (`- [ ] …`). Tick a step (`- [x]`) when it's done, so the open steps in [[Plans]] stay accurate; a finished plan gets `status: done`.
```yaml
---
displayName: Supervisor plan
tags: plan
status: active           # active, done
date: "2026-10-21"
spec: "[[superpowers/specs/2026-10-21-topic-design|Topic design]]"
---
```

**Component** (`Architecture/<Name>.md`): one part of the system as it is, or as a spec plans it. The page name's last part is the box label in diagrams, so name it plainly (`Architecture/Desktop proxy`). The body says in a few sentences what it does, how it's reached, and which files implement it.
```yaml
---
tags: component
status: built            # built, planned (in a spec, not built), external (not in this repository)
partOf: "[[Architecture/Desktop]]"   # its container, if any; no label in the link
uses:                                 # what it depends on or talks to; no labels in these links
  - "[[Architecture/KasmVNC]]"
spec: "[[superpowers/specs/2026-10-21-workspace-sandbox-design|Workspace Sandbox design]]"
---
```
[[Architecture]] draws all components with `docs.architecture()`, and each component page shows its neighbourhood in its card. Both come from `partOf` and `uses` alone, so keep those accurate and never draw architecture diagrams by hand.

**Keep the architecture current.** When a change adds, removes, renames or rewires a part of the system (code, or a spec that plans one), update its component pages in the same change: add or remove the page, fix `partOf`/`uses`, flip `status` from `planned` to `built` when it lands, and update the files it names.

When a skill writes a new spec or plan, add this frontmatter (replacing any `Date:`/`Status:` lines and the `#` title, which `displayName` takes over) before handing it over, and update the status of an existing one when its state changes.

# Conventions
- The file name is the page's identity; `displayName` is its title. A page starts with a short summary and uses `#` for its sections.
- One line per paragraph (no hard wrapping); `-` bullets.
- Link pages with wiki links rooted at the space root, labelled with the display name: `[[superpowers/specs/2026-10-21-supervisor-design|Supervisor design]]`. Renaming a file means updating links to it.
- Images and other files for a page go in a folder named after the page (`superpowers/specs/2026-10-21-supervisor-design/desktops.png`) and are linked with ordinary relative Markdown (`![…](2026-10-21-supervisor-design/desktops.png)`), which works in SilverBullet and on GitHub.
- Keep the tag vocabulary small: `spec`, `plan`, `component`, `meta`. Add a new kind (with its tag page in [[CONFIG]]) only when several pages need it.

# Checking changes
After editing, check that queries return what you expect and that pages render, for example:
```sh
sb --space "Container Computer" query 'from p = index.contentPages "spec" select {page=p.name, title=p.displayName, status=p.status}' --json
sb --space "Container Computer" eval 'editor.navigate("Architecture")' && sb --space "Container Computer" screenshot architecture.png
```
