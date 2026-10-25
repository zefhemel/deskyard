Outcome-based iterations with no planning dates. Agents verify features against agreed criteria; the owner accepts milestone outcomes. The owner has deferred selecting or preparing a next milestone. No proposal below should be inferred from a discovery feature or an old implementation plan. See [[Operations manual]] for the working agreement.

${docs.orMessage(query[[from p = index.contentPages "milestone" order by p.name select {Milestone="[[" .. p.name .. "|" .. p.displayName .. "]]", Status=p.status, Outcome=p.outcome, Scope=p.features}]], "No milestones have been prepared or selected.")}
