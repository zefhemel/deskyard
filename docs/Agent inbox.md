Comments and other open mentions addressed to the project agent, collected from across the space. Use SilverBullet's **Comment: Add** command beside the relevant text and mention `@agent`. See [[Operations manual#Comments and agent inbox|The working agreement]]. This queue is read at agent startup and work checkpoints; it does not itself schedule or wake an agent.

Run `$sb-inbox` in the project chat to process this queue explicitly. The repository skill reads current comments, handles requests within scope, runs appropriate checks and reports anything needing your input.

Once handled, the agent records the outcome and removes the addressing mention while retaining the comment. SilverBullet's **Resolve** action deletes the entire comment. Completed tasks are omitted by the native mention API. Page-level recipients remain listed until removed; use comments for individual actionable requests.

${query[[
  from m = identity.mentions("agent").mentions
  order by m.page, m.pos
  select {
    Document = "[[" .. m.ref .. "|" .. m.page .. "]]",
    Request = m.snippet,
    Author = table.concat(m.by or {}, ", "),
  }
]]}
