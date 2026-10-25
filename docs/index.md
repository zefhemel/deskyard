Deskyard: persistent shared Linux desktops, operated through a browser and CLI. This is the owner dashboard; the [[Operations manual|Project operations manual]] explains how we work. Source records hold current state and the views below are derived from them.

The capability inventory is based on current code and scoped observations, not a fresh certification of every deployment or device. Agents verify features against agreed criteria; the owner accepts milestones. Next milestone selection is deferred at the owner's request.

# Needs your attention
${docs.orMessage(docs.attentionRows(), "No decision, review or acceptance request is currently recorded for you.")}

# Milestone status
${docs.orMessage(docs.milestoneRows(), "No active milestone. Next milestone selection is deferred; none has been prepared.")}

# Work in progress
${docs.orMessage(docs.workRows(), "No feature implementation is currently recorded as active. The loose ends below are not scheduled commitments.")}

# Current capabilities
${docs.orMessage(docs.capabilityRows(), "No current capability records are available.")}

# Loose ends
${docs.orMessage(docs.looseEndRows(), "No unscheduled loose ends are recorded.")}

# Documentation health
${docs.orMessage(docs.healthRows(), "No closed execution plans, obsolete specs or missing summary/next-action fields are flagged by the record checks. Native lint is verified separately.")}

The full [[Loose ends]] view also includes parked investigations. Requests to agents are in [[Agent inbox]]; they are separate from the decisions and reviews above. A satisfied mention can be cleared while its discussion is preserved until the author resolves it.

# Explore
- [[Features]]: current capability, maturity, acceptance and limitations.
- [[Milestones]]: outcome-based scope and acceptance, without planning dates.
- [[Architecture]]: components and dependencies as implemented.
- [[Specs]]: retained current design contracts and rationale.
- [[Plans]]: temporary active execution instructions; completed plans are purged.
- [[Spikes]]: scoped feasibility findings, including parked approaches.
- [[Verification]]: test/review evidence with environments and limits.
- [[Use cases]]: family-first needs and open product questions.
- [[Licensing]]: distribution obligations and outstanding release work.
- [[Testing/Current capability inventory|Current inventory audit]] and [[Testing/Project record schemas|SilverBullet workflow checks and feedback]].
- [[Operations manual|Project operations manual]], [[AGENTS|Documentation conventions]] and [[Dashboard views|Dashboard view definitions]].
