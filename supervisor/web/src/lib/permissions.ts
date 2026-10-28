// What the signed-in account may do, in one place: pages read these flags
// instead of re-deriving them from me.admin, me.share and the environment's
// level. The server enforces the same rules (auth.Can); these only decide
// what the UI offers.
import type { Environment, Me, Share } from "../api.ts";

type Who = Pick<Me, "username" | "admin" | "kind" | "share">;

/** What an account may do on one environment. */
export interface EnvironmentPermissions {
  /** Full access: the desktop's input, terminals, files, power, and the CLI. */
  use: boolean;
  /** Watching its desktop and nothing else (its configuration is blanked for us). */
  observeOnly: boolean;
  /** Admin: settings, who can use it, backups, upgrade and removal. */
  configure: boolean;
  /** Making shares of it: full access, and not a share (shares can't share). */
  share: boolean;
  /** Seeing its shares, as the Access tab and the viewers popover list them. */
  seeShares: boolean;
  /** WebDAV on its Access tab: admins switch it; others who use it and have a password see the address while it's on. */
  webdav: "switch" | "address" | null;
  /** Whether a share of it may be revoked or given a new link by us. */
  manageShare: (s: Pick<Share, "sharedBy">) => boolean;
}

/** An admin manages every share; anyone else the shares they made. */
export function mayManageShare(me: Pick<Me, "username" | "admin">, s: Pick<Share, "sharedBy">): boolean {
  return me.admin || s.sharedBy.username === me.username;
}

/** Whether the account has a WebDAV password of its own to make: password accounts that aren't shares. */
export function hasWebDAVPassword(me: Pick<Me, "kind" | "share">): boolean {
  return me.kind === "password" && !me.share;
}

export function can(me: Who, c: Pick<Environment, "level" | "webdav">): EnvironmentPermissions {
  const use = c.level !== "observe";
  return {
    use,
    observeOnly: !use,
    configure: me.admin,
    share: use && !me.share,
    seeShares: use,
    webdav: me.admin ? "switch" : c.webdav && use && hasWebDAVPassword(me) ? "address" : null,
    manageShare: (s) => mayManageShare(me, s),
  };
}

/** What an account may do outside any one environment. */
export interface AccountPermissions {
  /** Create environments, see conflicts, and manage images, storage, VPN and accounts. */
  administer: boolean;
  /** Make a WebDAV password on its account page. */
  webdavPassword: boolean;
}

export function accountCan(me: Pick<Me, "admin" | "kind" | "share">): AccountPermissions {
  return { administer: me.admin, webdavPassword: hasWebDAVPassword(me) };
}
