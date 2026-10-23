Design documents for **Deskyard**: persistent Linux desktops ("desktops") in containers, shared by people and AI agents, run by one supervisor binary (the repository's `README.md` explains how to use it). This folder is a SilverBullet space and reads fine as plain Markdown on GitHub too.

It holds use cases, the architecture, and specs and plans written with the superpowers brainstorming and writing-plans skills and kept in their folders:

- [[Use cases]]: family and small-company scenarios, shared needs and open questions; the family use case is the immediate priority.
- [[Specs]]: what we build and why, one design spec per topic, in `superpowers/specs/`.
- [[Plans]]: how a spec gets built, step by step, in `superpowers/plans/`.
- [[Architecture]]: the system's components, built and planned, one page each in `Architecture/`, with diagrams drawn from them.
- [[superpowers/spikes/2026-10-23-shared-folders|Dynamic shared folders on Mac and Linux investigation]]: filesystem permission limits, live mount feasibility, platform evidence and the recommended implementation approach.

Agents working here: read [[AGENTS]] first.

# Specs
${query[[
  from p = index.contentPages "spec"
  order by p.date desc
  select {
    Spec = "[[" .. p.name .. "|" .. (p.displayName or p.name) .. "]]",
    Status = p.status,
    Date = p.date,
  }
]]}

# Plans
${query[[
  from p = index.contentPages "plan"
  order by p.date desc
  select {
    Plan = "[[" .. p.name .. "|" .. (p.displayName or p.name) .. "]]",
    Status = p.status,
    Date = p.date,
  }
]]}
