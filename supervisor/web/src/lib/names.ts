// The names the supervisor accepts, as the UI checks them before asking.

/** Environments, volumes, remotes and repositories (environments.ValidName): 2–32 characters. */
export const RESOURCE_NAME = /^[a-z][a-z0-9-]{0,30}[a-z0-9]$/;

/** What a valid resource name looks like, shown when one isn't. */
export const resourceNameHint = "Use 2–32 lowercase letters, digits and dashes, starting with a letter and not ending with a dash.";

/** Accounts and shares. */
export const USERNAME = /^[a-z][a-z0-9._-]{0,31}$/;

export const usernameHint = "Lowercase letters, digits, dots, dashes and underscores, starting with a letter.";
