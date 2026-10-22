# Semantic search

Use when the user wants conceptual matches ("pages about …", other wording, another language) or pages related to a given page. Semantic search is a free feature of SilverBullet Server and Desktop, available in spaces where an administrator or the Desktop user chose a search model and the space didn't turn it off. Do not choose a model, change space settings, or start a download merely because it is missing.

Check what the space's search supports first:

```sh
sb --space notes eval 'search.status()' --json
```

The result is `{backend, model, capabilities = {modes, related}, ready, progress, reason}`. Semantic search is available when `capabilities.modes` contains `natural`; related pages are available when `capabilities.related` is true (false while the semantic index is still building). `backend` is null when search is unavailable, and `reason` says why. A `reason` while `ready` is true is a warning, not a failure.

```sh
sb --space notes eval 'search.find("soil drainage", {mode = "natural", limit = 10})' --json
sb --space notes eval 'search.related("Garden Notes", {limit = 10})' --json
```

Both return `{items, total, partial, notice}`. Each item has `page`, `pos`, `line`, `heading` (when known), `excerpt` and `highlights`; `pos` is the match's position in the page, and `highlights` are `[start, end]` UTF-16 offsets into `excerpt`. `search.related` returns one item per page, best first, with its closest passage. `partial` and `notice` signal an index that is still building: a pending index is not an empty result. Read the referenced page before making claims. `mode = "words"` (all words) and `mode = "phrase"` (exact phrase) are the full-text modes, better for names, codes and exact wording; `scope` (`pages`, `meta` or `all`) narrows either call.

`search.related` raises an error while related pages are unavailable or when the page isn't in the semantic index yet; check readiness with bounded retries when needed, and report the pending state if it persists. Always name the page explicitly: the CLI runtime's current page need not be the page visible to the user. If semantic search is unavailable, use `search.find` in `words` mode, SLIQ for structural questions, or ordinary file reading for content, explaining the narrower coverage.
