Scoped test, review and compatibility evidence. Results apply to the environments and limits documented in each record; a passing local check is not blanket platform certification.

${query[[
  from p = index.contentPages "verification"
  order by p.name
  select {
    Record = "[[" .. p.name .. "|" .. (p.displayName or p.name) .. "]]",
    Result = p.result, Scope = p.scope,
  }
]]}
