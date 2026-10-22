---
name: workspace-browser
description: "Use when you need a web browser inside the workspace sandbox: browsing, scraping, filling forms, or anything behind a login. Drives the shared, already-logged-in Chromium over CDP."
---

# The shared browser

One Chromium runs on the shared desktop with the debugging port
`http://127.0.0.1:9221`. It holds the human's logins (cookies live in
`~/.config/chromium-shared`). Attach to it; never launch your own browser,
and never use Playwright's `launch()`: a fresh browser has none of the
logins and the human can't see it.

## Attach with Playwright

```python
# /// script
# dependencies = ["playwright"]
# ///
from playwright.sync_api import sync_playwright

with sync_playwright() as p:
    browser = p.chromium.connect_over_cdp("http://127.0.0.1:9221")
    context = browser.contexts[0]  # the shared profile, with its logins
    page = context.new_page()      # a new tab the human can see
    page.goto("https://example.com")
    print(page.title())
```

Run it with `uv run script.py` (uv installs Playwright on first use; no
`playwright install` needed, since you're attaching, not launching).
Node works the same way: `chromium.connectOverCDP("http://127.0.0.1:9221")`.

- Open your own tab (`context.new_page()`) rather than taking over the
  human's current tab; close it when done.
- `browser.close()` on a CDP connection only disconnects; Chromium keeps
  running.
- Quick checks without Playwright: `curl -s http://127.0.0.1:9221/json/list`
  lists open tabs.
- If the browser isn't running (it only starts when needed, or the human
  closed it), connecting to 9221 starts it
  (the first connection takes a few seconds).

## Logins

When a page needs a login, 2FA or a CAPTCHA, bring that tab to the front
(`page.bring_to_front()`), tell the human which site and what is needed,
and wait for them to finish in the desktop. Poll the page (URL or a
selector) to notice when they're done, then continue. The session then
persists for future runs.

## When CDP isn't enough

Browser UI outside the page (permission prompts, the profile menu, file
pickers) isn't reachable over CDP: use the `workspace-computer-use` skill.
