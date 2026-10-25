---
displayName: Project record schema verification and SilverBullet feedback
tags: verification
result: passed
scope: Native project record schemas, typed relationships, editor lint response contracts and whole-space lint in the local SilverBullet runtime.
environment: SilverBullet Desktop 2.12.0-137-g3ee1f82de, Container Computer local folder space.
---
The eight project record types in [[CONFIG]] validate through SilverBullet's native object linting. [[AGENTS]] documents their fields and lifecycle. The live regression script `scripts/check-docs.lua` passes 55 checks and asserts an empty whole-space lint result. These checks validate metadata and relationships; they do not certify historical implementation claims or reconcile old task checkboxes.

# Reproduction and verification

From the repository root, run outside the sandbox:
```sh
sb --space "Container Computer" eval 'editor.invokeCommand("Space: Reindex")'
sb --space "Container Computer" eval 'system.reboot()'
sb --space "Container Computer" script --file scripts/check-docs.lua --json
sb --space "Container Computer" eval 'space.lint()' --json
```
Expected regression output is `{"checks":55,"diagnostics":[]}`; direct lint returns `[]`. The regression script exits unsuccessfully on an assertion, so callers need not infer success from a lint list printed with exit code zero.

Before schemas were installed, the first negative test failed because an invented feature status was accepted. Later tests caught missing metadata, incorrect relationship targets, missing outcome evidence, missing planned-component specs and the interactive editor response bug below. The schema suite includes valid records as well as negative fixtures and uses the actual native validators and `editor:lint` dispatcher rather than mocks. It creates no fixture pages.

The initial schema migration exposed two discovery features, eleven specs, twelve plans, thirty-one components, seven spikes and fifteen verification records before adding this record. No milestone was selected. The feature overview and feature card were visually inspected; the card error below was reproduced twice, then verified absent after the configuration correction. Markdown link checks and `git diff --check` passed. No Deskyard runtime code changed in this task, so Go, UI and Docker test suites were not run.

# SilverBullet feedback: malformed empty lint responses

This was a bug in the new project configuration, exposed by inconsistent handling between interactive editor lint and `space.lint()`. An `editor:lint` Space Lua callback returned `{}` for no findings. At the JavaScript boundary this became an object, not an empty diagnostic array. The editor flattened callback responses and attempted to treat that object as a diagnostic; feature navigation consistently displayed `Cannot read properties of null (reading 'endSide')`. Refreshing the UI alone did not resolve it.

Before correction, this actual event call returned `[[],[],[],[],{}]`:
```lua
local text = space.readPage("Features/Custom DNS")
return event.dispatch("editor:lint", {
  name="Features/Custom DNS",
  pageMeta={name="Features/Custom DNS"},
  text=text,
  tree=markdown.parseMarkdown(text),
})
```

The same configuration made `space.lint()` return an empty diagnostic list (`[]` in CLI output): the batch linter skips non-array listener results. A regression asserting that every returned listener batch validates as a JSON array failed on the malformed object. Changing the callback's no-findings return to `nil` removed that object from dispatched results, made the regression pass and removed the visible error after refreshing and navigating to the feature again.

Suggested SilverBullet improvement: validate custom lint results once at the event boundary and turn malformed results into an explicit diagnostic naming the listener or its definition. Both interactive and batch lint should report the same failure; neither should silently discard it. An editor-side defensive filter should prevent a malformed extension response from crashing CodeMirror.

![Feature navigation before correcting the malformed lint response](Project%20record%20schemas/endSide-before.png)

![Feature navigation after returning nil for no lint findings](Project%20record%20schemas/endSide-after.png)

# SilverBullet feedback: listener exceptions

An earlier callback incorrectly expected `e.text` instead of the wrapped payload's `e.data.text`. The configured callback existed, but dispatch omitted its rejected response. Consequently a whole-space lint could appear clean while that custom validator was not running. The negative editor-event regression caught the absent diagnostic. Correct payload handling made it pass.

Suggested improvement: batch lint should surface a failed `editor:lint` listener as an error containing its definition and exception, rather than relying only on console logs. The same applies to CLI callers using lint as a verification gate. This is separate from changing generic `sb eval` semantics: evaluating a diagnostic list successfully is not itself a failed command. A dedicated lint command or failure-on-diagnostics option would make scripting clearer.

# SilverBullet feedback: validation without a source range

Native object validation can return an error for an immediate record page lacking its required tag. For pages with no frontmatter, the validated page object has no range; the native object linter does not emit a diagnostic for that error. Several existing Testing pages initially had no frontmatter, so this mattered during migration.

The project now has a small `editor:lint` hook that reports missing frontmatter in typed folders at position zero. A regression dispatches a bare `Testing/Schema fixture` and requires the expected diagnostic. Suggested improvement: native object lint should fall back to a page-level position when an invalid object has no range.

# Scope and follow-ups

The project configuration workaround and regression checks are implemented here. SilverBullet itself and its CLI were not modified. Further improvements should be made in the SilverBullet repository with its own tests. Configuration refresh initially left stale callbacks until reindex and reload; the documented sequence above was verified to load the latest definitions, but this investigation did not isolate a separate refresh defect.

# Agent mention workflow

The shared `agent` identity is registered in [[CONFIG]] and [[Agent inbox]] queries the native `identity.mentions` API. [[Operations manual#Comments and agent inbox|The working agreement]] makes this a startup/checkpoint input and defines contextual replies and completion.

A temporary page containing an open mention in an HTML comment and a completed task addressed to the same identity was indexed in the live space. The native inbox returned exactly one matching row with `inComment = true`, excluding the completed task. Calling `index.resolveAtMention` with the fresh row's page, range and nickname and the `remove` action removed only the open addressing token; the comment text and completed task remained intact. The fixture was then deleted and the index refreshed. The final agent inbox contained zero open requests. The then-39-check schema regression suite and whole-space lint remain the repeatable configuration checks; this mention check used the real native API rather than a mock.

# Explicit inbox skill

The repository-scoped `.agents/skills/sb-inbox/SKILL.md` provides explicit invocation as `$sb-inbox`; its `agents/openai.yaml` disables implicit invocation. The skill delegates comment policy to [[Operations manual#Comments and agent inbox|The working agreement]]. The standard skill validator passed using its PyYAML dependency through uv. An independent read-only agent followed the skill and referenced guidance, queried the actual empty inbox twice, and reported no broken references or unnecessary edits. This covered the empty-inbox path; nonempty mention discovery, task filtering and preserving comment text were exercised separately above. At that checkpoint the project schema/lint suite reported 39 passing checks and zero diagnostics.

# Continued use and architecture maintenance

[[Operations manual#Keep architecture aligned with implementation|The architecture completion review]] now requires comparing component prose, interfaces, implementation paths and generated relationships to the actual code before handover. Root agent guidance makes the review discoverable and requires ongoing updates to this feedback record when SilverBullet or CLI behavior fails or surprises the agent. The operations manual distinguishes structural lint success from semantic accuracy and interactive rendering. No new SilverBullet defect was observed while making this documentation-only change; the previously recorded issues and workarounds remain the evidence-backed findings.


# Live owner dashboard and record cleanup

The regression suite now runs 52 checks, including actionable owner attention fields, the implemented/availability distinction and pure dashboard view cases. A negative fixture first demonstrated that an invalid attention blocking value was accepted; the schema correction rejects it. Live views separate current capabilities, actual implementation, unscheduled discovery and parked work without fabricating owner requests or milestones. All dashboard output remains live; no baked sections are used.

The cleaned inventory contains 25 features: 16 implemented capabilities (15 available and one experimental), eight unscheduled discovery records and one parked investigation. Seven retained specs describe current contracts and link to feature records. Twelve historical implementation plans were removed after carrying durable evidence and decisions forward; the temporary cleanup plan was also purged. Removed image-authoring designs and mockups no longer compete with the current implementation. Mobile prototype media and Selkies performance observations remain with scoped verification records. No next milestone was prepared.

Link and attachment checks, named feature source paths, generated architecture views and the live dashboard are checked separately from native metadata lint. This work changes project documentation/configuration only; pre-existing runtime edits remain in the working tree and are not newly certified by the record checks.

# Owner trial of comment replies and resolution

The owner posted “test, are you there?” addressed to the agent in a real page comment. The agent discovered it, wrote a contextual reply and removed the addressing mention while retaining the discussion. A later read showed the entire comment gone. The owner explicitly confirmed clicking **Resolve** after seeing the reply. This is expected author-driven comment removal, not an observed SilverBullet failure. The final native agent inbox was empty. The trial complements the fixture-based nonempty workflow and independent empty-inbox skill check above.


# Frontmatter presentation revision

All project record previews now use the same labelled metadata panel, with full populated record fields and relationship links. Verification previews include scope and environment; owner requests include the blocking state. Architecture component previews retain their generated clickable neighbourhood diagram inside the panel. Fields render as literal Markdown, keeping written Lua directives inert while retaining page links. YAML editing remains available through the native frontmatter menu or Alt-click.

The first visual retest showed the extra fields but no border/background even though native schema checks passed. Inspection of SilverBullet's frontmatter CSS found a more specific rule clearing block-widget borders and backgrounds. The project Space Style now targets `#sb-main .cm-editor .sb-frontmatter-preview .project-frontmatter`, overriding that rule without `!important`. Reindex/reload and visual retest showed the tinted panel and accent border. This was a project styling integration issue, not a demonstrated SilverBullet or CLI defect.

Three new native-runtime regression cases cover feature metadata previously omitted by the summary renderer, verification scope/environment and component dependency metadata. The new feature case failed against the previous renderer before implementation; the suite now passes 55 checks with zero diagnostics. Visual feature, verification and component checks remain separate from these structural assertions. No product architecture responsibilities or dependencies changed: this revision affects only the documentation preview, and existing graph relationships are preserved. `CONFIG`, the documentation manual and this feedback record describe the change.
