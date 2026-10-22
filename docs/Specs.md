What we build and why: one design spec per topic, tagged `#spec`, in `superpowers/specs/`. A spec's status is `draft` (being discussed), `approved` (agreed, not built yet), `implemented` or `superseded`. How a spec gets built lives in [[Plans]].

${query[[
  from p = index.contentPages "spec"
  order by p.date desc
  select {
    Spec = "[[" .. p.name .. "|" .. (p.displayName or p.name) .. "]]",
    Status = p.status,
    Date = p.date,
    Plan = p.plan,
  }
]]}
