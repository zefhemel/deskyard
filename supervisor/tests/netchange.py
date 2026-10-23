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
context counts and logs such failures (pytest shows the log with a failed
test). Loopback UI bootstrap GETs and login use Playwright's request transport,
which is independent of Chromium's network-change detector. Each request
is sent once; other mutations, downloads, live streams and Selkies' client
stay on the browser's transport. Waits for late frame loads use `failures`.
"""

from __future__ import annotations

import re
import time
import weakref
from urllib.parse import urlsplit

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


_failures: weakref.WeakKeyDictionary = weakref.WeakKeyDictionary()
# Match bootstrap requests at the routing layer: even route.fallback() needs
# Python callbacks, so matching media updates would pause them during sleeps.
LOOPBACK = re.compile(
    r"^https?://(?:127\.0\.0\.1|localhost|\[::1\])(?::\d+)?/"
    r"(?:api/(?:me|host|desktops|images|recipes|login)"
    r"|(?:assets|src|@vite|node_modules)/[^?]*"
    r"|desktops/[^/?]+|[^/?]*)/?(?:\?[^#]*)?$"
)


def _request_transport(route):
    request = route.request
    if request.resource_type not in {"document", "script", "stylesheet", "xhr", "fetch"} or (
            request.method != "GET" and urlsplit(request.url).path != "/api/login"):
        route.fallback()
        return
    # No retries here: replaying a mutation after an uncertain response
    # could run it twice. Pass redirects back to the browser as redirects.
    try:
        route.fulfill(response=route.fetch(max_redirects=0))
    except Error:
        route.abort("failed")


def failures(page: Page) -> int:
    """How many of the requests of page's browser context failed so so far:
    for waits that a page loading after its goto (a frame it adds) can fail."""
    return _failures.get(page.context, 0)


def _logging(new):
    def run(browser: Browser, *args, **kwargs):
        made = new(browser, *args, **kwargs)
        ctx = made.context if isinstance(made, Page) else made

        def note(request):
            if _changed(request):
                _failures[ctx] = _failures.get(ctx, 0) + 1
                print(f"{CHANGED}: {request.method} {request.url}")

        ctx.on("requestfailed", note)
        ctx.route(LOOPBACK, _request_transport)
        return made

    return run


_goto, _reload = Page.goto, Page.reload
Page.goto = _retrying(_goto)
Page.reload = _retrying(_reload)
Browser.new_context = _logging(Browser.new_context)
Browser.new_page = _logging(Browser.new_page)
