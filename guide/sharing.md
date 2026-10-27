# Accounts and sharing

An account is a username with either a password or none: a **token account**
signs in through a single-use **login link** instead. There are no email
resets or SSO. Admins create both kinds on the Accounts page (or with
`deskyard account create`), optionally with a description, an expiry and the
admin flag, and choose which environments each account may use, at one of two
levels:

- **Full**: everything a regular account could do before: the desktop, its
  terminal, files, processes, the `deskyard` CLI, power actions and sharing.
- **Observe**: watch the desktop and nothing else. The supervisor drops an
  observer's keyboard, mouse, clipboard and resize messages before they reach
  the desktop, and refuses its terminal, files, processes, CLI and settings.
  Observers show as "observing" next to the viewers. Observers don't see the
  mouse pointer: it isn't part of the picture, and each controlling browser
  draws its own.

| | Admin | Full access | Observe |
| --- | --- | --- | --- |
| See and open environments | all | granted ones | granted ones (view only) |
| Start, stop, restart | all | granted ones | no |
| Terminal, files, processes, `deskyard` CLI | all | granted ones | no |
| Share an environment | all | granted ones (not from a share) | no |
| Create, remove, change settings, upgrade, reclaim memory | yes | no | no |
| Accounts, access, image catalog, volumes | yes | no | no |

Everyone can change their own password (password accounts), see their
signed-in devices under **Account and devices** and sign each out, and
**Sign out everywhere**, which ends every browser login of the account
(from the account menu).

**Share** in the bar above an environment's desktop, under Shares on its
**Access** tab, or `deskyard share create` makes a token account with one
grant, full or observe-only, with an optional time limit, and shows a link
such as `https://host/link#dyl_…` to send through a chat app. The link works
once: opening it shows who it signs in as and a **Sign in** button, so link
previews and scanners can't use it up. A used, replaced or revoked link
answers "This link was already used or replaced". Unused links don't expire;
**New link** replaces the previous one. **Revoke** (under Shares on the Access
tab, or next to the share in the desktop bar's viewers panel) ends a share at
once, closing its desktop streams, terminals and CLI connections. Its maker
and admins can revoke it. A share also ends when the person who made it loses
full access to the environment or is deleted. Shares can't share further.
Admins make login links for other token accounts with **Login link** on the
Accounts page (`deskyard account link USER`).

Everyone with access to an environment opens the **same desktop**: one screen,
one mouse, one keyboard, like two people at one desk. The desktop follows
the browser window of whoever resized last (observers don't resize it). The
supervisor shows who's viewing on the tiles, the details page and the desktop
page's bar, but nothing arbitrates between people and CLI clients driving it at
the same time.

Removing an account, revoking a share, removing a grant or lowering it to
observe takes effect at once: the connections the new access doesn't allow
close, and a recreated account of the same name never honours the old
account's cookies, links or devices. Lowering or removing a grant on the
Accounts page (or with `deskyard account update`) also ends the account's
browser logins; changing access on the environment's Access tab doesn't.
