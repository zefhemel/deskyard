// The WebDAV drive of an environment, as the page the user is on sees it:
// the supervisor can't tell whether an HTTPS proxy is in front of it, the
// browser can.

type Place = Pick<Location, "origin" | "protocol" | "hostname">;

/** The address to give Finder or a WebDAV app for environment `name`. */
export function davAddress(name: string, at: Place = location): string {
  return `${at.origin}/${encodeURIComponent(name)}/dav/`;
}

/** Whether passwords sent to the drive would cross the network readable: plain HTTP, not from this machine. */
export function davInTheClear(at: Place = location): boolean {
  return at.protocol === "http:" && !["localhost", "127.0.0.1", "[::1]"].includes(at.hostname);
}
