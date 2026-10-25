Unscheduled product work and verification gaps, reconciled against current implementation. These records are not an approved priority order, a selected milestone or a claim that historical bugs remain unfixed. Click a record for its current boundary, acceptance criteria and next action. [[Features]] contains the implemented baseline.

# Open discovery and follow-ups
${docs.orMessage(docs.looseEndRows(), "No open follow-ups recorded.")}

# Parked investigations
${docs.orMessage(query[[from p = index.contentPages "feature" where p.status == "parked" order by p.name select {Record="[[" .. p.name .. "|" .. p.displayName .. "]]", Current=p.summary, Revisit=p.next}]], "No parked investigations.")}
