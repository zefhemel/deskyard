How container-computer is put together, one page per component in `Architecture/`, tagged `#component`. Today it is one thing, the [[Architecture/Computer]]: a container with a shared desktop, browser and agent tooling, started with `docker compose`. The [[Architecture/Supervisor]] is designed (see [[superpowers/specs/2026-10-21-supervisor-design|Supervisor design]]) but not built: one binary that runs and shares many computers, and once it exists the only way to run one. Agents elsewhere reach a computer through it with the [[Architecture/Remote CLI]].

The diagram is drawn from the components' frontmatter: `partOf` puts a component inside its container, `uses` draws an arrow. Solid boxes are built, dashed ones planned, dotted ones outside this repository. Each component's page shows the part of the diagram around it.

${docs.architecture()}

# Components
${query[[
  from p = index.contentPages "component"
  order by p.partOf, p.name
  select {
    Component = "[[" .. p.name .. "|" .. string.match(p.name, "[^/]+$") .. "]]",
    ["Part of"] = p.partOf,
    Status = p.status,
  }
]]}
