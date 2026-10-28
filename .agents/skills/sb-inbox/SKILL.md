---
name: sb-inbox
description: Use when the user explicitly asks to process SilverBullet comments addressed to @agent or invokes $sb-inbox in the Deskyard repository.
---

# Process the SilverBullet agent inbox

Read the repository's `AGENTS.md` and the **Comments and agent inbox** section of `docs/Operations manual.md`. The operations manual is authoritative for the workflow; `docs/AGENTS.md` defines documentation conventions and validation; do not create a parallel comment-handling policy.

Treat explicit invocation as a request to process the pending inbox now, including requests on documents outside the current task. Preserve unfinished work in the chat. For large feature requests, follow the project's design/approval workflow and report the next decision rather than silently starting another implementation.

From the repository root, query the live SilverBullet space outside the sandbox:

```sh
sb --space "docs" eval 'identity.mentions("agent")' --json
```

Process all returned entries, consuming pagination if used. A failed command is not an empty inbox. Read each current document and surrounding comment before acting; snippets and offsets may be stale. Re-query after edits before using `index.resolveAtMention` with the fresh page, range and nickname. For page-level recipients, inspect the page's request; do not invent a comment range or automatically remove its recipient metadata.

Apply requests within the user's scope and existing permissions. Reply beside the request and remove only a satisfied addressing mention, preserving the comment text. Keep questions and unresolved requests open with a concise response identifying the needed input. Do not delete comments through Resolve. Avoid repeating the same pending response on repeated invocations.

Run the checks appropriate to any changes, including the project's native documentation check when docs change:

```sh
sb --space "docs" script --file scripts/check-docs.lua --json
```

Re-query once at the end. Report handled requests, remaining requests needing user input, and verification. If there are no entries, report that the inbox is empty without editing files or creating placeholder work. This invocation processes a batch; it does not install an automation or wait indefinitely for future comments.
