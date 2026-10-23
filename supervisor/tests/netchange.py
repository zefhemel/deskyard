"""Chromium on Linux and the desktops' networks.

Chromium watches the host's network interfaces, and every desktop has a
network of its own: creating or removing one adds or removes a bridge and a
veth pair, which Chromium takes as the network changing. Connections being
set up right then fail with net::ERR_NETWORK_CHANGED, even to 127.0.0.1: a
navigation, or a script, frame or fetch of the page. No switch turns that off,
and the tests create desktops all the time, in parallel. (Docker Desktop and
OrbStack keep the networks in their VM, so a Mac never sees this.)

So, for every test (conftest imports this): Page.goto and Page.reload load
the page again when it or something it loaded failed so, and every browser
context logs such failures, which pytest shows with a failed test.
"""

from __future__ import annotations

import time

from playwright.sync_api import Browser, Error, Page

CHANGED = "net::ERR_NETWORK_CHANGED"
TRIES = 5


def _changed(request) -> bool:
    return CHANGED in (request.failure or "")


def _retrying(load):
    def run(page: Page, *args, **kwargs):
        url = page.url
        for attempt in range(1, TRIES + 1):
            failed: list[str] = []

            def note(request):
                if _changed(request):
                    failed.append(request.url)

            page.on("requestfailed", note)
            try:
                if attempt > 1 and load is _reload:
                    response = _goto(page, url, **kwargs)  # not the error page's reload
                else:
                    response = load(page, *args, **kwargs)
            except Error as e:
                # The next try can be cut short by Chromium's own error page
                # for the last one still coming.
                again = CHANGED in str(e) or (attempt > 1 and "chrome-error://" in str(e))
                if not again or attempt == TRIES:
                    raise
                print(f"{load.__name__}: {str(e).splitlines()[0]}; loading the page again")
                _settle(page)
                continue
            finally:
                page.remove_listener("requestfailed", note)
            if not failed or attempt == TRIES:
                return response
            print(f"{load.__name__}: {CHANGED} for {failed}; loading the page again")
            _settle(page)

    return run


def _settle(page: Page):
    """Lets a failed load end (in Chromium's error page) before the next."""
    time.sleep(0.5)
    try:
        page.wait_for_load_state("load", timeout=5000)
    except Error:
        pass


def _logging(new):
    def run(browser: Browser, *args, **kwargs):
        made = new(browser, *args, **kwargs)
        ctx = made.context if isinstance(made, Page) else made
        ctx.on("requestfailed", lambda r: _changed(r) and print(f"{CHANGED}: {r.method} {r.url}"))
        return made

    return run


_goto, _reload = Page.goto, Page.reload
Page.goto = _retrying(_goto)
Page.reload = _retrying(_reload)
Browser.new_context = _logging(Browser.new_context)
Browser.new_page = _logging(Browser.new_page)
