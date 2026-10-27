"""Integration tests: a real supervisor against the local container engine.

DESKYARD_TEST_MODE=direct (default) runs the binary on this machine;
DESKYARD_TEST_MODE=container runs the supervisor image (DESKYARD_TEST_SUPERVISOR_IMAGE,
default deskyard:test, built from this checkout) with the socket and the
data folder mounted at the same path. CC_TEST_IMAGE is the desktop image
(default deskyard-debian-xfce:test, built from the debian-xfce target in images/Dockerfile;
these suites don't build it, so build it from this checkout first, as
`make test-engine` does, or name one: an older build has the wrong contract).
DESKYARD_TEST_SUBNET is the desktops' subnet pool (default 10.215.0.0/20);
under xdist each worker runs its own supervisor on its own slice of it.

What runs once, before any worker starts: the web UI is rebuilt if its
sources are newer than internal/web/dist, and the supervisor and the
deskyard command are built (or, in container mode, the supervisor's image).

The `desktop` fixture takes a desktop from a small pool that each
supervisor creates ahead of the tests (DESKYARD_TEST_POOL, default 1; 0 is
off): every test still gets a fresh desktop, without waiting for it. A
supervisor never creates two desktops at once (its subnet pick isn't
locked): the pool's creates and the tests' take turns.

Tests marked `serial` are left out under xdist and run in a second pass
with --only-serial. The desktop page's tests run per browser: Firefox is
skipped up front where Playwright's Firefox can't start; WebKit is marked
`webkit`, which the default run leaves out.
"""

from __future__ import annotations

import ipaddress
import json
import math
import os
import shutil
import socket
import subprocess
import time
import uuid
from pathlib import Path

import pytest
import requests

import netchange  # noqa: F401  (Chromium's pages survive a desktop's network coming or going)

ROOT = Path(__file__).resolve().parent.parent
REPO = ROOT.parent
IMAGE = os.environ.get("CC_TEST_IMAGE", "deskyard-debian-xfce:test")
MODE = os.environ.get("DESKYARD_TEST_MODE", "direct")
SUPERVISOR_IMAGE = os.environ.get("DESKYARD_TEST_SUPERVISOR_IMAGE", "deskyard:test")
# Another checkout's tests can run at the same time on a different subnet.
SUBNET = os.environ.get("DESKYARD_TEST_SUBNET", "10.215.0.0/20")
POOL_SIZE = int(os.environ.get("DESKYARD_TEST_POOL", "1"))
ADMIN_PASSWORD = "admin-password"
BIN = ROOT / "bin"
SUPERVISOR_BIN = BIN / "deskyard-supervisor-test"
CLI_BIN = BIN / "deskyard-test"


def free_port() -> int:
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def docker(*args, check=True) -> subprocess.CompletedProcess:
    return subprocess.run(["docker", *args], capture_output=True, text=True, check=check)


# --- once per run, in the main process --------------------------------------------------------

def worker() -> str | None:
    return os.environ.get("PYTEST_XDIST_WORKER")


def web_is_stale() -> bool:
    dist = ROOT / "internal" / "web" / "dist" / "index.html"
    if not dist.exists():
        return True
    web = ROOT / "web"
    sources = [web / "index.html", web / "package.json", web / "package-lock.json", web / "vite.config.ts"]
    sources += [p for d in ("src", "public") for p in (web / d).rglob("*") if p.is_file()]
    return max(p.stat().st_mtime for p in sources if p.exists()) > dist.stat().st_mtime


def build_once():
    if MODE == "container":
        subprocess.run(["docker", "build", "-q", "-t", SUPERVISOR_IMAGE, str(ROOT)], check=True, stdout=subprocess.DEVNULL)
    else:
        # The UI the supervisor embeds: never test against an old build of it.
        if web_is_stale():
            target = ["web"] if not (ROOT / "web" / "node_modules").exists() else []
            if target:
                subprocess.run(["make", "web"], cwd=ROOT, check=True)
            else:
                subprocess.run(["npm", "run", "build"], cwd=ROOT / "web", check=True, stdout=subprocess.DEVNULL)
        subprocess.run(["go", "build", "-o", str(SUPERVISOR_BIN), "./cmd/deskyard-supervisor"], cwd=ROOT, check=True)
    subprocess.run(["go", "build", "-o", str(CLI_BIN), "./cmd/deskyard"], cwd=ROOT, check=True)


def firefox_problem() -> str:
    """Why Playwright's Firefox can't start here ("" if it can)."""
    from playwright.sync_api import Error, sync_playwright
    try:
        with sync_playwright() as p:
            p.firefox.launch().close()
        return ""
    except Error as e:
        # The browser's own complaint, if it said one ("Could not find profile folder.").
        errs = [line.split("[err]", 1)[1].strip() for line in str(e).splitlines() if "[err]" in line]
        errs = [x for x in errs if "headless mode" not in x]
        return (errs[0] if errs else str(e).strip().splitlines()[0])[:200]


def pytest_addoption(parser):
    parser.addoption("--only-serial", action="store_true",
                     help="run only the tests marked serial (they're left out under xdist)")


def pytest_configure(config):
    if worker() or config.option.collectonly:
        return
    build_once()
    # Workers inherit this.
    os.environ.setdefault("DESKYARD_TEST_FIREFOX", firefox_problem() or "ok")


def pytest_collection_modifyitems(config, items):
    only = config.getoption("--only-serial")
    parallel = bool(worker())
    firefox = os.environ.get("DESKYARD_TEST_FIREFOX", "ok")
    keep, drop = [], []
    for item in items:
        serial = item.get_closest_marker("serial") is not None
        if (only and not serial) or (parallel and serial):
            drop.append(item)
            continue
        keep.append(item)
        cs = getattr(item, "callspec", None)
        if firefox != "ok" and cs and cs.params.get("browser_name") == "firefox":
            item.add_marker(pytest.mark.skip(reason=f"Playwright's Firefox doesn't start here: {firefox}"))
    if drop:
        config.hook.pytest_deselected(items=drop)
        items[:] = keep


# --- the supervisor ----------------------------------------------------------------------------

def my_subnet() -> str:
    """This process's slice of SUBNET: under xdist, one per worker."""
    count = int(os.environ.get("PYTEST_XDIST_WORKER_COUNT", "1"))
    if not worker() or count <= 1:
        return SUBNET
    net = ipaddress.ip_network(SUBNET, strict=False)
    bits = math.ceil(math.log2(count))
    return str(list(net.subnets(prefixlen_diff=bits))[int(worker().removeprefix("gw"))])


class Client(requests.Session):
    """A signed-in API client (same-origin headers like a browser's fetch)."""

    def __init__(self, base: str, before_create=None):
        super().__init__()
        self.base = base
        self.headers["Origin"] = base
        self.before_create = before_create

    def request(self, method, url, *a, **kw):
        kw.setdefault("timeout", 60)
        if url.startswith("/"):
            url = self.base + url
        if self.before_create and method.upper() == "POST" and url.split("?")[0] == self.base + "/api/environments":
            self.before_create()
        return super().request(method, url, *a, **kw)

    def login(self, username: str, password: str) -> "Client":
        r = self.post("/api/login", json={"username": username, "password": password})
        assert r.status_code == 200, r.text
        return self

    def wait_job(self, job: str, timeout=300) -> dict:
        with self.get(f"/api/jobs/{job}", stream=True, timeout=timeout) as r:
            last = None
            for line in r.iter_lines():
                if line.startswith(b"data: "):
                    last = json.loads(line[6:])
                    if last["status"] != "running":
                        return last
        return last

    def start_create(self, name: str, **kw) -> str:
        """Starts creating a desktop; returns the job."""
        r = self.post("/api/environments", json={"name": name, "image": IMAGE, **kw})
        assert r.status_code == 202, r.text
        return r.json()["job"]

    def create(self, name: str, **kw) -> dict:
        st = self.wait_job(self.start_create(name, **kw))
        assert st["status"] == "done", st
        return self.desktop(name)

    def desktop(self, name: str) -> dict:
        r = self.get(f"/api/environments/{name}")
        assert r.status_code == 200, r.text
        return r.json()


class Supervisor:
    def __init__(self, tmp: Path):
        self.data = tmp / "data"
        self.data.mkdir(parents=True)
        self.port = free_port()
        self.base = f"http://127.0.0.1:{self.port}"
        self.subnet = my_subnet()
        self.proc = None
        self.container = None
        self.pool: DesktopPool | None = None
        self._instance = ""

    def start(self):
        env = {
            "DESKYARD_DATA_DIR": str(self.data), "DESKYARD_ADMIN_PASSWORD": ADMIN_PASSWORD,
            "DESKYARD_IMAGE": IMAGE, "DESKYARD_SUBNET": self.subnet,
            "DESKYARD_ACTIVITY_PRESENCE": "12s",
        }
        if MODE == "container":
            self.container = f"deskyard-test-supervisor-{uuid.uuid4().hex[:6]}"
            args = ["run", "-d", "--name", self.container, "-p", f"127.0.0.1:{self.port}:7343",
                    "-v", "/var/run/docker.sock:/var/run/docker.sock", "-v", f"{self.data}:{self.data}"]
            for k, v in env.items():
                args += ["-e", f"{k}={v}"]
            docker(*args, SUPERVISOR_IMAGE)
        else:
            self.log = open(self.data.parent / "supervisor.log", "a")
            self.proc = subprocess.Popen([str(SUPERVISOR_BIN)], env={**os.environ, **env, "DESKYARD_LISTEN": f"127.0.0.1:{self.port}"},
                                         stdout=self.log, stderr=subprocess.STDOUT)
        deadline = time.time() + 60
        while time.time() < deadline:
            try:
                requests.get(self.base + "/healthz", timeout=1)
                return
            except requests.RequestException:
                time.sleep(0.3)
        raise TimeoutError("supervisor didn't start:\n" + self.logs())

    def read(self, path: Path) -> str:
        """A file the supervisor wrote. In container mode through the
        container: on a Linux engine its files are root's, some owner-only."""
        if not self.container:
            return path.read_text()
        r = docker("exec", self.container, "cat", str(path), check=False)
        if r.returncode != 0:
            raise (FileNotFoundError if "No such file" in r.stderr else RuntimeError)(f"reading {path}: {r.stderr}")
        return r.stdout

    def instance(self) -> str:
        """The supervisor's instance ID ("" before its first start). Kept
        once read, so teardown has it after the container is gone."""
        if not self._instance:
            try:
                self._instance = self.read(self.data / "state" / "instance").strip()
            except FileNotFoundError:
                pass
        return self._instance

    def logs(self) -> str:
        if self.container:
            r = docker("logs", self.container, check=False)
            return r.stdout + r.stderr
        return (self.data.parent / "supervisor.log").read_text()

    def stop(self):
        if self.pool:
            self.pool.settle()  # no create cut off halfway
        if self.proc:
            self.proc.terminate()
            self.proc.wait(10)
        if self.container:
            docker("rm", "-f", self.container, check=False)

    def client(self) -> Client:
        # One create at a time on a supervisor: a test's waits for the pool's.
        return Client(self.base, before_create=lambda: self.pool and self.pool.settle())

    def admin(self) -> Client:
        return self.client().login("admin", ADMIN_PASSWORD)


class DesktopPool:
    """Desktops created ahead: the supervisor creates the next while a test
    uses the last. Each is handed out once, and removed after its test.

    A supervisor creates one desktop at a time here: the pool starts a
    create only when it hands one out (no test create is running then),
    and a test's own create waits for the pool's (Client.before_create)."""

    def __init__(self, admin: Client, size: int):
        self.admin, self.size = admin, size
        self.waiting: list[tuple[str, str]] = []  # (name, job)

    def _start(self):
        name = "t" + uuid.uuid4().hex[:6]
        self.waiting.append((name, self.admin.start_create(name)))

    def take(self) -> dict:
        if not self.waiting:
            self._start()
        name, job = self.waiting.pop(0)
        st = self.admin.wait_job(job)
        # Then the next, one at a time.
        while len(self.waiting) < self.size:
            self.settle()
            self._start()
        # (None: the job is gone with a restarted supervisor; settle() let it finish first.)
        assert st is None or st["status"] == "done", st
        d = self.admin.desktop(name)
        assert d["state"] == "running", d
        return d

    def settle(self):
        """Waits until the pool's creates are done."""
        for _, job in self.waiting:
            self.admin.wait_job(job)

    def close(self):
        self.settle()
        for name, _ in self.waiting:
            self.admin.delete(f"/api/environments/{name}?files=1")
        self.waiting = []


@pytest.fixture(scope="session")
def supervisor(tmp_path_factory):
    # A folder the container engine can mount (on macOS: under the home folder).
    base = Path.home() / ".cache" / "deskyard-tests" / uuid.uuid4().hex[:8]
    base.mkdir(parents=True)
    sup = Supervisor(base)
    sup.start()
    yield sup
    instance = sup.instance()  # before stop: container mode reads it through the container
    sup.stop()
    # Remove what the tests created, and nothing else on the engine: this
    # supervisor's containers and networks carry its instance label, and any
    # other managed container it left (a helper) mounts its data folder.
    ours = set()
    if instance:
        label = f"label=deskyard.instance={instance}"
        ours.update(docker("ps", "-a", "--format", "{{.Names}}", "--filter", label, check=False).stdout.split())
    for name in docker("ps", "-a", "--format", "{{.Names}}", "--filter", "label=deskyard.managed=true", check=False).stdout.split():
        mounts = docker("inspect", "-f", "{{range .Mounts}}{{.Source}} {{end}}", name, check=False).stdout
        if str(sup.data) in mounts:
            ours.add(name)
    for name in ours:
        docker("rm", "-f", name, check=False)
    if instance:
        for net in docker("network", "ls", "--format", "{{.Name}}", "--filter", label, check=False).stdout.split():
            docker("network", "rm", net, check=False)
    docker("run", "--rm", "-v", f"{base}:/b", "alpine:3.22", "rm", "-rf", "/b/data", check=False)
    shutil.rmtree(base, ignore_errors=True)


@pytest.fixture(scope="session")
def admin(supervisor) -> Client:
    return supervisor.admin()


@pytest.fixture(scope="session")
def pool(supervisor, admin, request):
    # Not for the serial pass: a desktop starting beside a timing test is load.
    p = DesktopPool(admin, 0 if request.config.getoption("--only-serial") else POOL_SIZE)
    supervisor.pool = p
    yield p
    supervisor.pool = None
    p.close()


@pytest.fixture
def name(request) -> str:
    """The test's desktop name: its `desktop`'s, if it has one."""
    if "desktop" in request.fixturenames:
        d = request.getfixturevalue("pool").take()
        request.node.pooled_desktop = d
        return d["name"]
    return "t" + uuid.uuid4().hex[:6]


@pytest.fixture
def desktop(admin, name, request):
    """A running desktop, removed (with its files) afterwards."""
    yield request.node.pooled_desktop
    admin.delete(f"/api/environments/{name}?files=1")


@pytest.fixture(scope="session")
def cli() -> str:
    """The deskyard command, built from this checkout."""
    return str(CLI_BIN)


@pytest.fixture
def remote(cli, admin, desktop, name, tmp_path):
    """Runs the deskyard command against the test's desktop, as an agent elsewhere:
    remote(*args), signed in (`deskyard auth`) to the supervisor as a token account
    (remote.username) with full access to the desktop, which is the server's
    default environment. remote.client names its credential, as the desktop's
    agents and forwards list it."""
    from helpers import login_link

    user = "c" + uuid.uuid4().hex[:5]
    r = admin.post("/api/accounts", json={"username": user, "grants": [{"environment": name, "level": "full"}]})
    assert r.status_code == 201, r.text
    env = {**os.environ, "XDG_CONFIG_HOME": str(tmp_path / "config"), "HOME": str(tmp_path)}
    for k in ("DESKYARD_URL", "DESKYARD_CREDENTIAL", "DESKYARD_ENVIRONMENT"):
        env.pop(k, None)

    def run(*args, input=None, check=True):
        r = subprocess.run([cli, *args], capture_output=True, env=env, input=input, timeout=120)
        if check and r.returncode != 0:
            raise AssertionError(f"deskyard {args} failed ({r.returncode}): {r.stderr.decode()}")
        return r

    run("auth", login_link(admin, user), "--name", "test")
    run.name = name
    run.env = env
    run.username = user
    run.client = admin.get(f"/api/accounts/{user}/credentials").json()[0]["name"]
    yield run
    admin.delete(f"/api/accounts/{user}")


@pytest.fixture
def forwards():
    """Starts `deskyard forward`s (helpers.Forward); stops them afterwards."""
    from helpers import Forward

    started = []

    def start(*a, **kw):
        f = Forward(*a, **kw)
        started.append(f)
        return f

    yield start
    for f in started:
        f.stop()


@pytest.fixture
def local_http():
    """A local HTTP server on a free port answering with a known body."""
    import http.server
    import threading

    body = b"hello from the agent's machine"

    class Handler(http.server.BaseHTTPRequestHandler):
        def do_GET(self):
            self.send_response(200)
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def log_message(self, *a):
            pass

    srv = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    srv.body = body
    srv.port = srv.server_address[1]
    yield srv
    srv.shutdown()
