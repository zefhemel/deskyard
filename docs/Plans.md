How a spec gets built: one implementation plan per spec, tagged `#plan`, in `superpowers/plans/`, linking its spec in `spec`. A plan's status is `active` or `done`; its steps are tasks, ticked as they're done.

${query[[
  from p = index.contentPages "plan"
  order by p.date desc
  select {
    Plan = "[[" .. p.name .. "|" .. (p.displayName or p.name) .. "]]",
    Status = p.status,
    Spec = p.spec,
    Date = p.date,
  }
]]}

# Open steps
${query[[
  from t = index.tasks()
  where not t.done and t.page:startsWith("superpowers/plans/")
  order by t.page
  select {
    Plan = "[[" .. t.page .. "]]",
    Step = t.name,
  }
]]}
