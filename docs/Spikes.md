Feasibility investigations and their conclusions. A concluded investigation does not mean its subject is implemented; designs and evidence remain linked from the underlying records.

${query[[
  from p = index.contentPages "spike"
  order by p.name
  select {
    Record = "[[" .. p.name .. "|" .. (p.displayName or p.name) .. "]]",
    Status = p.status, Conclusion = p.conclusion,
  }
]]}
