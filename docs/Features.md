Current and desired capabilities, improvements and defects. `implemented` records code that exists; `delivered` additionally records verified feature acceptance. Availability is independent: `available`, `experimental` or `unavailable`. Read each record's limits and evidence before treating it as deployment-ready. [[Loose ends]] lists unscheduled work; no next milestone has been selected.

# Current capability inventory
${docs.orMessage(docs.capabilityRows(), "No current capability records.")}

# All feature records
${query[[from p = index.contentPages "feature" order by p.name select {Feature="[[" .. p.name .. "|" .. p.displayName .. "]]", Stage=p.status, Availability=p.availability, Summary=p.summary}]]}
