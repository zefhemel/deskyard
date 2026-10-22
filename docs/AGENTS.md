---
tags: meta
---
Operating manual for agents (and people) adding or changing pages in this space: the design documents of container-computer. `CLAUDE.md` points here; keep this the only manual.

# The space
- The space root is `docs/` in the container-computer repository. It's registered in SilverBullet Desktop as **Container Computer**: use `sb --space "Container Computer" …` for queries and checks.
- Pages are plain Markdown and must stay readable on GitHub: put essential facts in prose, and use queries only for overviews.
- [[index]] is the entry page. [[CONFIG]] holds the space's Lua configuration. `screenshot.png` is the desktop screenshot the repository README shows.

# Kinds of pages
Specs and plans are written with the superpowers skills (brainstorming, writing-plans) and stay where those skills put them, under their dated file names. What makes them SilverBullet pages is their frontmatter: the tag is what [[Specs]], [[Plans]] and [[index]] query, and `displayName` is how they're shown in links, the page picker and those lists.

**Spec** (`superpowers/specs/YYYY-MM-DD-<topic>-design.md`): what we build and why.
```yaml
---
displayName: Computer Manager design
tags: spec
status: draft            # draft, approved, implemented, superseded
date: "2026-10-21"       # when the spec was written
plan: "[[superpowers/plans/2026-10-21-topic|Topic plan]]"   # once a plan exists
---
```

**Plan** (`superpowers/plans/YYYY-MM-DD-<topic>.md`): how a spec gets built, as steps written as tasks (`- [ ] …`). Tick a step (`- [x]`) when it's done, so the open steps in [[Plans]] stay accurate; a finished plan gets `status: done`.
```yaml
---
displayName: Computer Manager plan
tags: plan
status: active           # active, done
date: "2026-10-21"
spec: "[[superpowers/specs/2026-10-21-topic-design|Topic design]]"
---
```

When a skill writes a new spec or plan, add this frontmatter (replacing any `Date:`/`Status:` lines and the `#` title, which `displayName` takes over) before handing it over, and update the status of an existing one when its state changes.

# Conventions
- The file name is the page's identity; `displayName` is its title. A page starts with a short summary and uses `#` for its sections.
- One line per paragraph (no hard wrapping); `-` bullets.
- Link pages with wiki links rooted at the space root, labelled with the display name: `[[superpowers/specs/2026-10-21-computer-manager-design|Computer Manager design]]`. Renaming a file means updating links to it.
- Images and other files for a page go in a folder named after the page (`superpowers/specs/2026-10-21-computer-manager-design/computers.png`) and are linked with ordinary relative Markdown (`![…](2026-10-21-computer-manager-design/computers.png)`), which works in SilverBullet and on GitHub.
- Keep the tag vocabulary small: `spec`, `plan`, `meta`. Add a new kind (with its tag page in [[CONFIG]]) only when several pages need it.

# Checking changes
After editing, check that queries return what you expect and that pages render, for example:
```sh
sb --space "Container Computer" query 'from p = index.contentPages "spec" select {page=p.name, title=p.displayName, status=p.status}' --json
sb --space "Container Computer" eval 'editor.navigate("Specs")' && sb --space "Container Computer" screenshot specs.png
```
