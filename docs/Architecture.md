How Deskyard is put together, one page per component in `Architecture/`, tagged `#component`. The [[Architecture/Supervisor]] (one Go binary, see [[superpowers/specs/2026-10-21-supervisor-design|Supervisor design]]) runs and shares [[Architecture/Desktop]]s: containers with a shared desktop, browser and desktop tools. People use them in the browser or from their terminal with the [[Architecture/Remote CLI]].

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
