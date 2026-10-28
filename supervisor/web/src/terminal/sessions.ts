// What the tab strip needs to know about a shared terminal session before
// ending it for everyone.

const shells = new Set(["bash", "sh", "zsh", "fish", "dash"]);

/** Whether a pane's foreground command is an idle shell (`-bash` is a login shell). */
export function isShell(command: string): boolean {
  return shells.has(command.replace(/^-/, ""));
}

/** Clients other than this page: `clients` counts this page's own attachment too. */
export function others(session: { clients: number }, attached: boolean): number {
  return Math.max(0, session.clients - (attached ? 1 : 0));
}

/** × asks first when someone else is attached or a program other than the shell runs. */
export function needsConfirm(session: { clients: number; command: string }, attached: boolean): boolean {
  return others(session, attached) > 0 || !isShell(session.command);
}

/** A session name: 1–64 letters, digits, underscores or dashes, starting with a letter or digit. */
export const validSession = (s: string) => /^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/.test(s);
