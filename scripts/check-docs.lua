-- Run with: sb --space "Container Computer" script --file scripts/check-docs.lua --json
-- Uses SilverBullet's real object validator and editor lint event; creates no pages.
local checks = 0
local function check(kind, fields, valid)
  local p = { tag = "page", tags = {kind}, name = "Schema fixture", ref = "Schema fixture", displayName = "Schema fixture" }
  for k, v in pairs(fields) do p[k] = v end
  local err = index.validateObjects(p.name, {p})
  assert((err == nil) == valid, "Check " .. (checks + 1) .. " (" .. kind .. "): unexpected validation result: " .. (err and err.error or "accepted invalid metadata"))
  checks = checks + 1
end
check("feature", {status="invented"}, false)
check("feature", {status="idea", kind="capability", outcome="Select a resolver", acceptance={"Resolver selection is independent of VPN"}}, true)
check("feature", {status="idea", kind="capability", outcome="Select a resolver", acceptance={}}, false)
check("feature", {status="delivered", kind="capability", outcome="Select a resolver", acceptance={"Resolver works"}}, false)
check("milestone", {status="proposed", outcome="A usable outcome", acceptance={"Demonstrate the outcome"}, features={"[[superpowers/specs/2026-10-23-desktop-vpn-design]]"}}, false)
check("milestone", {status="proposed", outcome="A usable outcome", acceptance={"Demonstrate the outcome"}, features={"[[Features/Custom DNS]]"}}, true)
check("milestone", {status="proposed", outcome="A usable outcome", acceptance={"Demonstrate the outcome"}, features={"[[Features/Custom DNS]]"}, date="2027-01-01"}, false)
check("spec", {status="approved", plan="not a wiki link"}, false)
check("plan", {status="cancelled"}, true)
check("component", {status="built", uses={"[[Missing component]]"}}, false)
check("component", {status="built", uses={"[[Specs]]"}}, false)
check("component", {status="built", partOf="[[Schema fixture]]"}, false)
check("spike", {status="concluded"}, false)
check("spike", {status="concluded", conclusion="A full desktop cannot resume on the tested stack"}, true)
check("verification", {result="passed", scope="A specific tested behavior"}, true)
check("verification", {result="complete", scope="A specific tested behavior"}, false)
check("verification", {result="partial"}, false)

check("feature", {status="idea", kind="capability", outcome=" ", acceptance={"A criterion"}}, false)
check("feature", {status="idea", kind="capability", outcome="A result", acceptance={"A criterion"}, dependsOn="[[Features/Custom DNS]]"}, false)
check("feature", {status="idea", kind="capability", outcome="A result", acceptance={"A criterion"}, dependsOn={"[[Features/Custom DNS]]"}}, true)
check("feature", {status="idea", kind="capability", outcome="A result", acceptance={"A criterion"}, tags={"feature", "milestone"}}, false)
check("milestone", {status="achieved", outcome="A result", acceptance={"A criterion"}, features={"[[Features/Custom DNS]]"}}, false)
check("component", {status="planned"}, false)
check("component", {status="planned", spec="[[superpowers/specs/2026-10-23-desktop-vpn-design]]"}, true)
check("component", {status="built", uses={"[[Architecture/Desktop]]", "[[Architecture/Desktop]]"}}, false)
check("verification", {result="passed", scope="A specific tested behavior", status="complete"}, false)
check("spike", {status="concluded", conclusion=" "}, false)
check("meta", {}, true)

-- Owner requests must be actionable and machine-readable, not loose metadata.
check("plan", {status="active", attention={kind="decision", request="Choose the scope", recommendation="Keep the current scope", blocking="yes"}}, false)
check("plan", {status="active", attention={kind="decision", request="Choose the scope", recommendation="Keep the current scope", blocking=true}}, true)
check("plan", {status="active", attention={kind="unknown", request="Choose", recommendation="Keep", blocking=false}}, false)
check("plan", {status="active", attention={kind="review", recommendation="Keep", blocking=false}}, false)
check("plan", {status="active", attention={kind="review", request=" ", recommendation="Keep", blocking=false}}, false)
check("plan", {status="active", attention={kind="review", request="Review", recommendation="Keep", blocking=false, typo=true}}, false)
check("feature", {status="implemented", kind="capability", outcome="A result", acceptance={"A criterion"}, availability="available", summary="Current code exists", next="Verify the declared acceptance"}, true)
check("feature", {status="discovery", kind="capability", outcome="A result", acceptance={"A criterion"}, availability="wishful"}, false)

local text = "---\ntags: feature\ndisplayName: Invalid feature\nstatus: impossible\n---\nInvalid metadata must be reported in the editor.\n"
local batches = event.dispatch("editor:lint", {name="Features/Schema fixture", pageMeta={name="Features/Schema fixture"}, text=text, tree=markdown.parseMarkdown(text)})
local caught = false
for _, batch in ipairs(batches) do
  if type(batch) == "table" then
    for _, d in ipairs(batch) do
      if d.source == "objects" and d.severity == "error" then caught = true end
    end
  end
end
assert(caught, "editor:lint did not report invalid feature metadata")
checks = checks + 1
local bare = "A verification record without frontmatter must not escape lint."
local bareBatches = event.dispatch("editor:lint", {name="Testing/Schema fixture", pageMeta={name="Testing/Schema fixture"}, text=bare, tree=markdown.parseMarkdown(bare)})
local missingCaught = false
for _, batch in ipairs(bareBatches) do
  if type(batch) == "table" then
    for _, d in ipairs(batch) do
      if d.source == "project-records" and d.severity == "error" then missingCaught = true end
    end
  end
end
assert(missingCaught, "editor:lint did not report missing record frontmatter")
checks = checks + 1
for kind, _ in pairs(docs.recordTypes) do
  local err = jsonschema.validateSchema(index.tagSchema(kind))
  assert(err == nil, kind .. ": invalid schema: " .. tostring(err))
  checks = checks + 1
end

-- The interactive editor flattens listener responses into diagnostics. A Lua
-- empty table crosses the JS boundary as {}, not [], and crashes CodeMirror.
local validText = space.readPage("Features/Custom DNS")
local validBatches = event.dispatch("editor:lint", {name="Features/Custom DNS", pageMeta={name="Features/Custom DNS"}, text=validText, tree=markdown.parseMarkdown(validText)})
for _, batch in ipairs(validBatches) do
  assert(jsonschema.validateObject({type="array"}, batch) == nil, "A clean-page lint listener returned an object instead of a diagnostic array; the editor cannot render it")
end
checks = checks + 1

-- Dashboard rows separate owner requests and group features as done, in progress and not started.
local records = {
  {name="Features/Built", displayName="Built", tags={"feature"}, status="implemented", availability="available", summary="Works in code", outcome="Useful", next="Check device coverage"},
  {name="Features/Idea", displayName="Idea", tags={"feature"}, status="discovery", availability="unavailable", outcome="Future outcome", next="Design scope", attention={kind="decision", request="Choose behavior", recommendation="Prefer explicit policy", blocking=true}},
  {name="Features/Parked", displayName="Parked", tags={"feature"}, status="parked", availability="unavailable", outcome="Deferred", next="Revisit later"},
  {name="Features/Active", displayName="Active", tags={"feature"}, status="building", availability="experimental", summary="Partial", outcome="Useful", next="Verify fix"},
  {name="Features/Shipped", displayName="Shipped", tags={"feature"}, status="delivered", availability="available", summary="Verified", outcome="Useful"},
}
local attention = docs.attentionRows(records)
assert(#attention == 1 and attention[1].Request == "Choose behavior" and attention[1].Blocking == "Yes", "Dashboard owner requests must expose the exact decision and blocking state")
local done = docs.doneRows(records)
assert(#done == 1 and done[1].Availability == "available", "Dashboard done group must hold only delivered features")
local inProgress = docs.inProgressRows(records)
assert(#inProgress == 2 and inProgress[1].Stage == "building" and inProgress[1].Next == "Verify fix" and inProgress[2].Stage == "implemented", "Dashboard in-progress group must hold building, then implemented features")
local notStarted = docs.notStartedRows(records)
assert(#notStarted == 1 and notStarted[1].Next == "Design scope", "Dashboard not-started group must exclude built, delivered and parked features")
assert(#docs.attentionRows({}) == 0, "Empty owner inbox must not fabricate requests")
checks = checks + 5

-- Frontmatter previews must expose metadata omitted by the earlier summary cards.
local featurePreview = docs.card("Feature", {
  name="Features/Preview", displayName="Preview", tags={"feature"}, status="discovery",
  kind="improvement", availability="unavailable", outcome="Desired outcome", summary="Current boundary",
  acceptance={"Observable criterion"}, limitations={"Known limit"}, next="Next step",
  components={"[[Architecture/VPN runtime]]"}, dependsOn={"[[Features/Custom DNS]]"},
  evidence={"[[Testing/Current capability inventory]]"},
  attention={kind="input", request="Supply context", recommendation="Use the existing setup", blocking=false},
})
local featureMarkdown = widget.toMarkdown(featurePreview)
for _, value in ipairs({"Frontmatter", "improvement", "Desired outcome", "Current boundary", "Observable criterion", "Known limit", "Next step", "[[Architecture/VPN runtime]]", "[[Features/Custom DNS]]", "[[Testing/Current capability inventory]]", "Supply context", "Use the existing setup", "No"}) do
  assert(featureMarkdown:find(value, 1, true), "Frontmatter preview omitted " .. value)
end
assert(table.includes(featurePreview.cssClasses or {}, "project-frontmatter"), "Record preview must have a distinct frontmatter panel")
checks = checks + 1
local evidencePreview = docs.card("Verification", {name="Testing/Preview", tags={"verification"}, result="partial", scope="Scoped check", environment="Local runtime", features={"[[Features/Custom DNS]]"}})
local evidenceMarkdown = widget.toMarkdown(evidencePreview)
for _, value in ipairs({"partial", "Scoped check", "Local runtime", "[[Features/Custom DNS]]"}) do
  assert(evidenceMarkdown:find(value, 1, true), "Verification preview omitted " .. value)
end
checks = checks + 1
local componentPreview = docs.componentCard({name="Architecture/VPN runtime", tags={"component"}, status="built", uses={"[[Architecture/Engine adapter]]"}})
assert(table.includes(componentPreview.cssClasses or {}, "project-frontmatter"), "Component preview must share the frontmatter panel")
assert(widget.toMarkdown(componentPreview):find("[[Architecture/Engine adapter]]", 1, true), "Component preview omitted dependency metadata")
checks = checks + 1

local diagnostics = space.lint()
assert(#diagnostics == 0, "Space lint returned " .. #diagnostics .. " diagnostics; run space.lint() to inspect them")
return {checks=checks, diagnostics=diagnostics}
