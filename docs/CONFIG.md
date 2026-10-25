#meta

Space configuration and native lint schemas for project records (see [[AGENTS]] for the field contract):
- an icon wherever they're listed or linked;
- a labelled, bordered frontmatter panel showing populated record fields and relationship links; use its ⋯ menu or Alt-click to edit YAML; components also retain their neighbourhood diagram;
- `docs.architecture()`, the diagram of all components, drawn with the Diagrams library ([[Library/silverbullet-diagram/Mermaid]]) from the components' `partOf` and `uses` frontmatter;
- each record tag opens its index; metadata and typed relationships are validated by the native object linter;
- feature and milestone planning is date-free; dated historical records keep their authorship/observation dates.

```space-lua
docs = docs or {}

-- A shared recipient for project feedback addressed to coding agents.
identity.define {
  name = "agent",
  description = "Project agent: review requests and comments across the space",
}

-- Shared metadata panel. Keep fields visible rather than summarizing them away.
function docs.card(kind, p)
  local body = "**" .. kind .. " · Frontmatter**\n\n**" .. (p.displayName or string.match(p.name, "[^/]+$")) .. "**"
  local function field(label, value)
    if value ~= nil then
      if type(value) == "table" then
        if #value == 0 then return end
        body = body .. "\n\n**" .. label .. "**\n- " .. table.concat(value, "\n- ")
      else
        body = body .. "\n\n**" .. label .. ":** " .. tostring(value)
      end
    end
  end
  field("Tags", type(p.tags) == "table" and table.concat(p.tags, ", ") or p.tags)
  field("Status", p.status)
  field("Result", p.result)
  field("Kind", p.kind)
  field("Availability", p.availability)
  field("Written / observed", p.date)
  field("Current", p.summary)
  field("Outcome", p.outcome)
  field("Scope", p.scope)
  field("Environment", p.environment)
  field("Conclusion", p.conclusion)
  field("Next action", p.next)
  field("Acceptance criteria", p.acceptance)
  field("Limitations", p.limitations)
  field("Part of", p.partOf)
  field("Uses", p.uses)
  field("Components", p.components)
  field("Depends on", p.dependsOn)
  field("Features", p.features)
  field("Milestone", p.milestone)
  field("Design spec", p.spec)
  field("Implementation plan", p.plan)
  field("Supersedes", p.supersedes)
  field("Evidence", p.evidence)
  if p.attention then
    field("Needs your " .. p.attention.kind, p.attention.request)
    field("Recommendation", p.attention.recommendation)
    field("Blocking", p.attention.blocking and "Yes" or "No")
  end
  body = body .. "\n\n*Metadata · Use ⋯ → Edit source or Alt-click to edit YAML.*"
  return widget.new {markdown=body, display="block", evaluate=false, cssClasses={"project-frontmatter"}}
end

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

-- Components use the same panel, retaining the generated clickable diagram.
function docs.componentCard(p)
  local card = docs.card("Component", p)
  local diagram = docs.architecture(p.name)
  return widget.new {
    display = "block",
    cssClasses = {"project-frontmatter"},
    parts = {
      widget.markdownBlock(card.markdown, {evaluate=false}),
      "\n\n",
      diagram,
      "\n\n",
      widget.markdownBlock("*Solid: built. Dashed: planned. Dotted: outside this repository. Click a box to open it.*", {evaluate=false}),
    },
  }
end

-- Project records use native JSON Schemas and tag validators. Invalid records
-- remain indexed: lint reports mistakes without hiding work from dashboards.
local textField = { type = "string", minLength = 1, pattern = "\\S" }
local pageLink = { type = "string", pattern = "^\\[\\[[^\\[\\]#|]+(?:\\|[^\\[\\]]+)?\\]\\]$" }
local links = { type = "array", items = pageLink, minItems = 1, uniqueItems = true }
local criteria = { type = "array", items = textField, minItems = 1, uniqueItems = true }
local function enum(values) return { type = "string", enum = values } end

-- Relationships are owned by the referring record; reverse lists are queried.
-- Empty optional lists should be omitted, rather than stored as YAML null.
docs.recordTypes = {
  feature = {
    page = "Features", icon = "flag", required = {"displayName", "status", "kind", "outcome", "acceptance"},
    fields = {status=enum {"idea", "discovery", "ready", "building", "implemented", "delivered", "parked", "cancelled"}, kind=enum {"capability", "improvement", "defect"}, outcome=textField, acceptance=criteria, availability=enum {"available", "experimental", "unavailable"}, limitations=criteria, components=links, dependsOn=links, evidence=links},
    relations = {components="component", dependsOn="feature", evidence="verification"},
  },
  milestone = {
    page = "Milestones", icon = "target", required = {"displayName", "status", "outcome", "acceptance", "features"},
    fields = {status=enum {"proposed", "active", "achieved", "cancelled"}, outcome=textField, acceptance=criteria, features=links, evidence=links},
    relations = {features="feature", evidence="verification"},
  },
  spec = {
    page = "Specs", icon = "file-text", required = {"displayName", "status"},
    fields = {status=enum {"draft", "approved", "implemented", "superseded", "cancelled"}, plan=pageLink, features=links, supersedes=links},
    relations = {plan="plan", features="feature", supersedes="spec"},
  },
  plan = {
    page = "Plans", icon = "check-square", required = {"displayName", "status"},
    fields = {status=enum {"draft", "active", "done", "cancelled", "superseded"}, spec=pageLink, features=links, milestone=pageLink},
    relations = {spec="spec", features="feature", milestone="milestone"},
  },
  component = {
    page = "Architecture", icon = "box", required = {"status"},
    fields = {status=enum {"built", "planned", "external"}, partOf=pageLink, uses=links, spec=pageLink, features=links},
    relations = {partOf="component", uses="component", spec="spec", features="feature"},
  },
  spike = {
    page = "Spikes", icon = "search", required = {"displayName", "status"},
    fields = {status=enum {"active", "concluded", "superseded"}, conclusion=textField, features=links, spec=pageLink},
    relations = {features="feature", spec="spec"},
  },
  verification = {
    page = "Verification", icon = "check-circle", required = {"displayName", "result", "scope"},
    fields = {result=enum {"passed", "partial", "failed", "inconclusive"}, scope=textField, environment=textField, features=links, spec=pageLink, plan=pageLink, milestone=pageLink, components=links},
    relations = {features="feature", spec="spec", plan="plan", milestone="milestone", components="component"},
  },
  meta = { page = "AGENTS", icon = "settings", required = {}, fields = {}, relations = {} },
}

local function pageTags(p)
  if type(p.tags) == "table" then return p.tags end
  local tags = {}
  if type(p.tags) == "string" then
    for t in p.tags:gmatch("[^,%s]+") do table.insert(tags, t) end
  end
  return tags
end

local function frontmatter(text)
  local yamlText = text:match("^%-%-%-\r?\n(.-)\r?\n%-%-%-")
  if not yamlText then return {} end
  return yaml.parse(yamlText)
end

-- Read the target's saved frontmatter, not a potentially stale index entry.
-- Native object validation already uses the source's current editor contents.
local function validateRecord(kind, p)
  local def = docs.recordTypes[kind]
  for field, targetKind in pairs(def.relations) do
    for _, value in ipairs(asList(p[field])) do
      local target = linkTarget(value)
      if target == p.name then return field .. ": a record cannot refer to itself" end
      if not target or not space.pageExists(target) then return field .. ": missing page " .. (target or tostring(value)) end
      local ok, meta = pcall(function() return frontmatter(space.readPage(target)) end)
      if not ok or type(meta) ~= "table" then return field .. ": cannot read target metadata for " .. target end
      if not table.includes(pageTags(meta), targetKind) then return field .. ": " .. target .. " must be tagged #" .. targetKind end
    end
  end
  if (kind == "feature" and p.status == "delivered") or (kind == "milestone" and p.status == "achieved") then
    if type(p.evidence) ~= "table" or #p.evidence == 0 then return "Completed outcomes require evidence links to verification records" end
  end
  if kind == "component" and p.status == "planned" and not p.spec then return "A planned component requires a spec" end
  if kind == "spike" and p.status == "concluded" and not p.conclusion then return "A concluded spike requires a conclusion" end
  if kind == "feature" or kind == "milestone" then
    for _, key in ipairs {"date", "due", "deadline", "startDate", "endDate", "targetDate"} do
      if p[key] ~= nil then return "Roadmap records are outcome-based; omit " .. key end
    end
  end
  if kind == "verification" and p.status ~= nil then return "Use result for verification evidence, not status" end
end

-- Owner requests live with their source record and disappear when resolved.
local attention = {
  type="object", additionalProperties=false,
  required={"kind", "request", "recommendation", "blocking"},
  properties={kind=enum {"decision", "review", "input", "acceptance"}, request=textField, recommendation=textField, blocking={type="boolean"}},
}

for kind, def in pairs(docs.recordTypes) do
  local properties = {displayName=textField, summary=textField, next=textField, attention=attention}
  if kind ~= "feature" and kind ~= "milestone" then
    properties.date = {type="string", pattern="^\\d{4}-\\d{2}-\\d{2}$"}
  end
  for field, typ in pairs(def.fields) do properties[field] = typ end
  local recordKind = kind
  tag.define {
    name = kind, tagPage = def.page,
    schema = {type="object", properties=properties, required=#def.required > 0 and def.required or nil},
    validate = function(p) return validateRecord(recordKind, p) end,
    transform = function(o) o.pageDecoration = {icon=def.icon}; return o end,
    renderFrontmatter = kind == "component" and docs.componentCard or function(p)
      return docs.card(recordKind:gsub("^%l", string.upper), p)
    end,
  }
end

-- A missing tag must not bypass validation. Supporting assets (e.g. a nested
-- prototype README) and removed architecture identities remain ordinary pages.
local recordFolders = {
  {"Features/", "feature"}, {"Milestones/", "milestone"},
  {"superpowers/specs/", "spec"}, {"superpowers/plans/", "plan"},
  {"superpowers/spikes/", "spike"}, {"Testing/", "verification"},
}
tag.define {
  name = "page",
  validate = function(p)
    local tags = pageTags(p)
    local count = 0
    for _, t in ipairs(tags) do if docs.recordTypes[t] then count = count + 1 end end
    if count > 1 then return "Use one project record type per page" end
    for _, folder in ipairs(recordFolders) do
      if p.name and p.name:startsWith(folder[1]) then
        local leaf = p.name:sub(#folder[1] + 1)
        if not leaf:find("/", 1, true) and not table.includes(tags, folder[2]) then
          return "Pages in " .. folder[1] .. " require #" .. folder[2]
        end
      end
    end
  end,
}

-- Untagged pages without frontmatter have no native object range to underline.
-- No findings must return nil: {} becomes a JS object, not a diagnostic array.
event.listen {
  name = "editor:lint",
  run = function(event)
    local e = event.data
    if e.text:match("^%-%-%-\r?\n") then return nil end
    for _, folder in ipairs(recordFolders) do
      if e.name:startsWith(folder[1]) and not e.name:sub(#folder[1] + 1):find("/", 1, true) then
        return {{from=0, to=0, severity="error", source="project-records", message="Pages in " .. folder[1] .. " require frontmatter with #" .. folder[2]}}
      end
    end
    return nil
  end,
}
```


# Frontmatter panel appearance
Shared by every project record preview. Theme variables keep the panel distinct in light and dark themes; all source fields remain in YAML.

```space-style
#sb-main .cm-editor .sb-frontmatter-preview .project-frontmatter {
  border: 1px solid var(--top-border-color);
  border-left: 4px solid var(--ui-accent-color);
  border-radius: 6px;
  background: var(--subtle-background-color);
  padding: 0.75rem 1rem;
  margin: 0;
  font-size: 0.9em;
  line-height: 1.45;
  overflow-wrap: anywhere;
}
#sb-main .cm-editor .sb-frontmatter-preview .project-frontmatter p:not(svg p) { margin: 0.5em 0; }
#sb-main .cm-editor .sb-frontmatter-preview .project-frontmatter ul { margin: 0.25em 0 0.75em; padding-left: 1.4em; }
#sb-main .cm-editor .sb-frontmatter-preview .project-frontmatter > :first-child { margin-top: 0; }
#sb-main .cm-editor .sb-frontmatter-preview .project-frontmatter > :last-child { margin-bottom: 0; }
#sb-main .cm-editor .sb-frontmatter-preview .project-frontmatter svg { max-width: 100%; }
```
