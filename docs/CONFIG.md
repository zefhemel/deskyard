#meta

Space configuration for specs and plans:
- an icon wherever they're listed or linked;
- a card with title, status, date and the matching spec or plan instead of their frontmatter (click it to edit the YAML);
- clicking a `#spec` or `#plan` tag opens the page that collects them.

```space-lua
docs = docs or {}

-- The card shown in place of a spec's or plan's frontmatter.
function docs.card(kind, p)
  local parts = { kind .. ", " .. (p.status or "no status") }
  if p.date then table.insert(parts, "written " .. p.date) end
  local other = kind == "Spec" and p.plan or p.spec
  if other then
    table.insert(parts, (kind == "Spec" and "plan: " or "spec: ") .. other)
  end
  return widget.markdownBlock("**" .. (p.displayName or p.name) .. "**\n\n" .. table.concat(parts, " · "))
end

tag.define {
  name = "spec",
  tagPage = "Specs",
  transform = function(o)
    o.pageDecoration = { icon = "file-text" }
    return o
  end,
  renderFrontmatter = function(p) return docs.card("Spec", p) end,
}

tag.define {
  name = "plan",
  tagPage = "Plans",
  transform = function(o)
    o.pageDecoration = { icon = "check-square" }
    return o
  end,
  renderFrontmatter = function(p) return docs.card("Plan", p) end,
}
```
