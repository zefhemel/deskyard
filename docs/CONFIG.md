#meta

Space configuration for specs, plans and components:
- an icon wherever they're listed or linked;
- a card instead of their frontmatter (click it to edit the YAML): title, status, date and the matching spec or plan; for a component, its neighbourhood diagram;
- `docs.architecture()`, the diagram of all components, drawn with the Diagrams library ([[Library/silverbullet-diagram/Mermaid]]) from the components' `partOf` and `uses` frontmatter;
- clicking a `#spec`, `#plan` or `#component` tag opens the page that collects them.

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

-- The target page of a wiki link value: "[[Architecture/Caddy|Caddy]]" -> "Architecture/Caddy".
local function linkTarget(v)
  if type(v) ~= "string" then return nil end
  return (string.match(v, "^%[%[([^|%]]+)"))
end

local function asList(v)
  if type(v) == "table" then return v end
  if v then return { v } end
  return {}
end

-- A diagram of the components (pages tagged #component), drawn from their
-- frontmatter: `partOf` nests a component in its container, `uses` draws an
-- arrow. With `focus`, only that component and its neighbours: what it uses,
-- what uses it, its container, and for a container its parts.
function docs.architecture(focus)
  local all = query[[from index.contentPages "component"]]
  local containerOf = {}
  for _, p in ipairs(all) do containerOf[p.name] = linkTarget(p.partOf) end
  local keep = {}
  local function keepUses(p)
    for _, u in ipairs(asList(p.uses)) do
      local t = linkTarget(u)
      if t then keep[t] = true end
    end
  end
  for _, p in ipairs(all) do
    if not focus or p.name == focus then
      keep[p.name] = true
      keepUses(p)
      local c = linkTarget(p.partOf)
      if c then keep[c] = true end
    end
    if focus and linkTarget(p.partOf) == focus then
      keep[p.name] = true
      keepUses(p)
    end
    for _, u in ipairs(asList(p.uses)) do
      local t = linkTarget(u)
      if t and (t == focus or containerOf[t] == focus) then keep[p.name] = true end
    end
  end
  local pages = {}
  for _, p in ipairs(all) do
    if keep[p.name] then
      local copy = {}
      for k, v in pairs(p) do copy[k] = v end
      copy.diagramClass = p.name == focus and "focus" or p.status
      table.insert(pages, copy)
    end
  end
  local ir = mermaid.relationGraph {
    pages = pages, relations = { "uses" }, groupBy = "partOf", color = "diagramClass",
  }
  for _, el in ipairs(ir.elements) do
    if el.kind == "edge" then el.label = nil end
  end
  table.insert(ir.elements, { kind = "classDef", name = "planned", style = { ["stroke-dasharray"] = "5 4" } })
  table.insert(ir.elements, { kind = "classDef", name = "external", style = { ["stroke-dasharray"] = "2 3", color = "#6b7684" } })
  table.insert(ir.elements, { kind = "classDef", name = "focus", style = { ["stroke-width"] = "3px" } })
  return mermaid.diagram(ir)
end

-- A component's card: what it is, where it sits, and its neighbourhood diagram.
-- Links carry data-sb-ref, which the diagram widget's click handler follows.
function docs.componentCard(p)
  local function ref(target, label)
    return '<a href="#" data-sb-ref="' .. target .. '">' .. label .. "</a>"
  end
  local parts = { "Component, " .. (p.status or "no status") }
  local container = linkTarget(p.partOf)
  if container then
    table.insert(parts, "part of " .. ref(container, string.match(container, "[^/]+$")))
  end
  local spec = linkTarget(p.spec)
  if spec then
    table.insert(parts, "spec: " .. ref(spec, string.match(p.spec, "|([^%]]+)") or spec))
  end
  local diagram = docs.architecture(p.name)
  return widget.new {
    display = "block",
    html = "<div><p><b>" .. string.match(p.name, "[^/]+$") .. "</b><br>" .. table.concat(parts, " · ") ..
      "</p>" .. diagram.html ..
      "<p><small>Solid: built. Dashed: planned. Dotted: outside this repository. Click a box to open it.</small></p></div>",
    events = diagram.events,
  }
end

tag.define {
  name = "component",
  tagPage = "Architecture",
  transform = function(o)
    o.pageDecoration = { icon = "box" }
    return o
  end,
  renderFrontmatter = docs.componentCard,
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
