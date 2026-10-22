Design documents for **container-computer**: a Docker image that is a small, persistent Linux desktop shared by a human and an AI agent (the repository's `README.md` explains how to use it). This folder is a SilverBullet space and reads fine as plain Markdown on GitHub too.

It holds specs and plans, written with the superpowers brainstorming and writing-plans skills and kept in their folders, and the architecture:

- [[Specs]]: what we build and why, one design spec per topic, in `superpowers/specs/`.
- [[Plans]]: how a spec gets built, step by step, in `superpowers/plans/`.
- [[Architecture]]: the system's components, built and planned, one page each in `Architecture/`, with diagrams drawn from them.

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
