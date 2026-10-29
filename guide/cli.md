# The deskyard command

`deskyard` is already there inside every computer. To use it from your own
terminal, install it on Linux or macOS:

```bash
curl -fsSL https://raw.githubusercontent.com/zefhemel/deskyard/main/install.sh | sh
```

It downloads `deskyard-<os>-<arch>` from the
[edge release](https://github.com/zefhemel/deskyard/releases/tag/edge), checks
it against `SHA256SUMS` and installs it to `~/.local/bin` (or, if that isn't
writable, to `/usr/local/bin` with sudo, saying so first). Set
`DESKYARD_VERSION` to install another release tag, and `DESKYARD_INSTALL_DIR`
to install elsewhere. On Windows, download `deskyard-windows-amd64.exe` (or
`-arm64.exe`) from the edge release.

## Signing in

Sign it in once, in one of two ways:

- `deskyard login https://desktops.example.com/alice` prints a code and opens
  a browser page where you, signed in to Deskyard, check the code and press
  **Approve**. Any account with full access somewhere can do this. With an
  computer in the URL, that becomes the default.
- `deskyard auth LINK` signs in with a login link instead, without a browser:
  useful for agents and scripts. A full-access share's link works;
  observe-only links are for the browser.

Each signed-in CLI holds its own **client credential** (`dyc_…`, in
`~/.config/deskyard/servers.json`, mode 0600). It shows under **Signed-in
devices**, where you or an admin can sign it out; `deskyard logout` revokes it
on the server and forgets the server. A credential acts with its account's
access at the time of each request. Then:

```bash
deskyard computer list                    # the computers you can use
deskyard -c alice exec -- ls ~            # one command; exit code passed through
deskyard -c alice shell                   # interactive shell
deskyard -c alice terminal                # shared terminal; rejoin later
deskyard cp ./notes.md alice:/workspace/notes.md    # and back
deskyard -c alice screenshot > s.png      # every screen action works remotely
deskyard -c alice click 640 400
deskyard -c alice browser connect --port 9222   # the shared Chromium on local 127.0.0.1:9222
```

With `browser connect` running, Playwright's
`chromium.connect_over_cdp("http://127.0.0.1:9222")` (use
`browser.contexts[0]`, the shared profile), Puppeteer or chrome-devtools
tools work as if the browser were local. `-c COMPUTER` (or `--computer COMPUTER`)
goes before the command and picks one of the default server's computers;
`-c SERVER/COMPUTER` picks one on another signed-in server. `deskyard computer use alice`
saves typing `-e`, `deskyard server list` and `server default NAME` manage the
servers, and `DESKYARD_COMPUTER` (`COMPUTER` or `SERVER/COMPUTER`) or
`DESKYARD_URL` with `DESKYARD_CREDENTIAL` work without a config file. Inside an
computer, without `-c`, commands act on that computer directly. For
command help, use `deskyard --help`, `deskyard guide` (a short manual) and
`deskyard describe --json` (machine-readable command descriptions).

The CLI can do what its account may do, checked by the server on every
request: exec, shell, files, browser, forward and `computer start|stop|restart`
need full access to the computer, and `share` works where the account may
share. Admins also manage the server from it:

```bash
deskyard computer create bob --image ghcr.io/zefhemel/deskyard-debian-xfce:latest --grant sanne
deskyard computer access bob sanne=observe     # or =full, or =none
deskyard volume create photos
deskyard account create sanne --description "Contractor" --grant bob --expires 2w   # prints a login link
printf '%s' "$PASSWORD" | deskyard account create anna --password-stdin --admin
deskyard share create bob guest --observe --expires 1d                              # prints the link
deskyard share revoke guest
deskyard device list --account sanne
```

Removing an account, revoking a share or its device, its expiry, or losing the
grant closes the CLI's open connections at once. A computer's access also
dies with it: removing a computer and creating a new one with the same
name does not bring back its old grants.

## Port mapping

Reach a server on one machine from the other, with the same sign-in:

```bash
deskyard -c alice forward 3000 &      # your 127.0.0.1:3000 is localhost:3000 in the computer
deskyard -c alice forward -R 5173 &   # the computer's localhost:5173 is your 127.0.0.1:5173
```

The first lets the computer call a service running on your machine; the second lets you open a dev server running in the computer in your own browser. To use different port numbers, write `LOCAL:COMPUTER` for a plain mapping (`8080:3000`: your 8080 is the computer's 3000) and `COMPUTER:LOCAL` after `-R` (`-R 80:8080`: the computer's 80 is your 8080). Several mappings can share one command (at most 64 plain ones and 256 in all), and one signed-in CLI can hold four such links per computer. Everything goes over the existing HTTPS connection (no extra ports are opened on the engine host) and listens only on `127.0.0.1`. Only *listening* in the computer is limited to ports from 1024 up, so plain `forward 80` is refused while `-R 80:8080` works (the helper just dials). If the connection drops or the computer restarts, `deskyard` reconnects by itself with backoff (up to 30 s): `-R` local ports stay bound meanwhile, and plain mappings come back in the computer once the link is back. People watching the desktop see which ports your CLI maps (up to 64 per signed-in CLI are listed). "Linked to …" and the `out` lines are printed when the link is accepted, a moment before the helper in the computer is known to be running.

## Agents inside a computer

The desktop also ships skills for tools such as Claude Code and Codex.

**Inside a computer.** Install an agent into `/workspace` from its
terminal and run it there:

```bash
curl -fsSL https://claude.ai/install.sh | bash          # Claude Code
npm i -g @openai/codex                                   # Codex
```

Hermes Agent also runs inside a computer:

```bash
curl -fsSL https://hermes-agent.nousresearch.com/install.sh -o /tmp/hermes-install.sh
bash /tmp/hermes-install.sh --non-interactive --skip-browser --skip-computer-use
hermes config set skills.external_dirs '["/opt/workspace/skills"]'
LANG=C.UTF-8 hermes model   # choose a provider and sign in
LANG=C.UTF-8 hermes
```

This installs Hermes into `/workspace`, uses its local terminal backend,
and makes the image's three skills available to it. The optional Hermes
browser and computer-use downloads are skipped; the computer's shared
browser and `deskyard` command remain available through those skills.
The project space's Hermes Agent trial spike records the
verified versions, startup checks and limitations. A model conversation
still needs provider credentials.

Older desktop images export `UV_PYTHON_PREFERENCE`, which conflicts with
the installer's explicit Python flags. On those images, prefix the
`bash /tmp/hermes-install.sh ...` command with
`env -u UV_PYTHON_PREFERENCE`, or upgrade the desktop image.

The image links its skills into `~/.claude/skills`, `~/.codex/skills`,
`~/.agents/skills` and `~/.gemini/skills` on every start:

- `workspace-computer`: what this machine is, how to install things so they
  persist, what not to touch.
- `workspace-browser`: drive the shared Chromium over CDP (on
  `127.0.0.1:9221` inside); ask the human to log in.
- `workspace-computer-use`: the `deskyard` command.

They update with the image. To customise one, replace its link with a real
directory; it won't be touched again.
