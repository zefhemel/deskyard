// Where sign-in goes: a login link's code, a CLI login request's id, and
// the page to return to afterwards.

/**
 * The code of a login link, `dyl_<id>_<secret>`, from the /link page's
 * fragment (the browser never sends it to the server); "" for anything else.
 */
export function parseLinkFragment(hash: string): string {
  const m = /^#(dyl_[A-Za-z0-9]+_[A-Za-z0-9_-]+)$/.exec(hash);
  return m?.[1] ?? "";
}

/** The id of a CLI login request from the /cli-login page's fragment; "" for none. */
export function cliLoginId(hash: string): string {
  const m = /^#([A-Za-z0-9_-]{1,128})$/.exec(hash);
  return m?.[1] ?? "";
}

/** Only paths on this origin: never send someone elsewhere after signing in. */
export function safeNext(next: string | null): string {
  if (!next || !next.startsWith("/") || next.startsWith("//") || next.startsWith("/\\")) return "/";
  return next;
}
