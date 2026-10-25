---
displayName: Dashboard views
tags: meta
---
Derived views for [[index]], [[Features]], [[Milestones]] and [[Loose ends]]. These functions read source-record metadata; they never change project state or infer approval from a checkbox. [[AGENTS]] defines the fields and [[Operations manual]] defines their use. Views can also accept a scoped record collection for a feature or milestone page.

```space-lua
docs = docs or {}

function docs.records()
  return query[[from p = index.pages() where not p.name:startsWith("Library/") select p]]
end

local function isFeature(p)
  return type(p.tags) == "table" and table.includes(p.tags, "feature")
end

local function ref(p)
  return "[[" .. p.name .. "|" .. (p.displayName or p.name) .. "]]"
end

local function sorted(rows)
  table.sort(rows, function(a, b) return a.Record < b.Record end)
  return rows
end

function docs.attentionRows(records)
  local rows = {}
  for _, p in ipairs(records or docs.records()) do
    if p.attention then
      table.insert(rows, {Record=ref(p), Need=p.attention.kind, Request=p.attention.request, Recommendation=p.attention.recommendation, Blocking=p.attention.blocking and "Yes" or "No"})
    end
  end
  table.sort(rows, function(a, b)
    if a.Blocking ~= b.Blocking then return a.Blocking == "Yes" end
    return a.Record < b.Record
  end)
  return rows
end

function docs.capabilityRows(records)
  local rows = {}
  for _, p in ipairs(records or docs.records()) do
    if isFeature(p) and (p.availability == "available" or p.availability == "experimental") then
      table.insert(rows, {Record=ref(p), Availability=p.availability, Current=p.summary or p.outcome})
    end
  end
  return sorted(rows)
end

function docs.workRows(records)
  local rows = {}
  for _, p in ipairs(records or docs.records()) do
    if isFeature(p) and p.status == "building" then
      table.insert(rows, {Record=ref(p), Current=p.summary or p.outcome, Next=p.next or "Record the next action"})
    end
  end
  return sorted(rows)
end

function docs.looseEndRows(records)
  local rows = {}
  for _, p in ipairs(records or docs.records()) do
    if isFeature(p) and (p.status == "idea" or p.status == "discovery" or p.status == "ready") then
      table.insert(rows, {Record=ref(p), Stage=p.status, Outcome=p.outcome, Next=p.next or "Record the next action"})
    end
  end
  return sorted(rows)
end

function docs.milestoneRows(records)
  local rows = {}
  for _, p in ipairs(records or docs.records()) do
    if type(p.tags) == "table" and table.includes(p.tags, "milestone") and p.status == "active" then
      table.insert(rows, {Record=ref(p), Outcome=p.outcome, Scope=p.features})
    end
  end
  return sorted(rows)
end

function docs.healthRows(records)
  local rows = {}
  for _, p in ipairs(records or docs.records()) do
    if type(p.tags) == "table" then
      if table.includes(p.tags, "plan") and (p.status == "done" or p.status == "cancelled" or p.status == "superseded") then
        table.insert(rows, {Record=ref(p), Action="Extract useful decisions/evidence, repair references and purge the closed plan"})
      elseif table.includes(p.tags, "spec") and (p.status == "superseded" or p.status == "cancelled") then
        table.insert(rows, {Record=ref(p), Action="Retain useful rationale and remove obsolete design"})
      elseif isFeature(p) and not p.summary then
        table.insert(rows, {Record=ref(p), Action="Record current capability and limitations"})
      elseif isFeature(p) and (p.status == "discovery" or p.status == "ready" or p.status == "building") and not p.next then
        table.insert(rows, {Record=ref(p), Action="Record the next action"})
      end
    end
  end
  return sorted(rows)
end

function docs.orMessage(rows, message)
  if #rows == 0 then return message end
  return rows
end
```
