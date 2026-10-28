// New account and Edit account: how it signs in (a password, or login links),
// description, admin, environments with their level, and expiry.
import { useState } from "preact/hooks";
import { api, type Account, type AccountChange, type Grant } from "../api.ts";
import { LevelPicks } from "../components/environment-fields.tsx";
import { notifyError } from "../components/toast.tsx";
import { Button, Check, Dialog, ErrorText, errorMessage, Field, Seg } from "../components/ui.tsx";
import { useLive } from "../live.tsx";
import { levelLabel } from "../lib/grants.ts";
import { expiresAt, expiryChange, type TimeLimit } from "../lib/time-limits.ts";
import { Expiry, TimeLimitField } from "./share.tsx";
import { USERNAME, usernameHint } from "../lib/names.ts";

type Kind = Account["kind"];

const kinds: { value: Kind; label: string }[] = [
  { value: "password", label: "Password" },
  { value: "token", label: "Login link" },
];

/** `link` is the new account's first login link, for a token account. */
export function AccountDialog({ account, onClose, onSaved }: { account: Account | null; onClose: () => void; onSaved: (link?: { username: string; link: string }) => void }) {
  const { environments } = useLive();
  const isNew = account === null;
  const share = account?.sharedBy ?? null;
  const [username, setUsername] = useState("");
  const [kind, setKind] = useState<Kind>(account?.kind ?? "password");
  const [password, setPassword] = useState("");
  const [description, setDescription] = useState(account?.description ?? "");
  const [admin, setAdmin] = useState(account?.admin ?? false);
  const [picked, setPicked] = useState<Grant[]>(account?.grants ?? []);
  // An existing account's expiry stays unless changed.
  const [changeExpiry, setChangeExpiry] = useState(isNew);
  const [limit, setLimit] = useState<TimeLimit>("none");
  const [date, setDate] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const who = isNew ? username || "they" : account.username;

  // Admins with a password never expire: the instance always keeps a password sign-in.
  const canExpire = !(admin && kind === "password");
  const when = expiresAt(limit, date);
  const badName = isNew && username !== "" && !USERNAME.test(username);
  const ready =
    (isNew ? USERNAME.test(username) && (kind === "token" || password !== "") : true) &&
    description.length <= 200 &&
    !(changeExpiry && canExpire && when === undefined);

  const save = async () => {
    if (!ready) return;
    setBusy(true);
    setError(null);
    try {
      const body: AccountChange = { description };
      if (!share) {
        body.grants = admin ? [] : picked;
        if (isNew || admin !== account.admin) body.admin = admin;
      }
      if (kind === "password" && password) body.password = password;
      const expires = expiryChange({ canExpire, changeExpiry, when, hadExpiry: !isNew && account.expires !== null });
      if (expires !== undefined) body.expires = expires;
      if (isNew) {
        await api.createAccount({ username, ...body });
        if (kind !== "token") return onSaved();
        // The account exists now: without its link, it can get one from the list.
        const link = await api.accountLink(username).catch((e: unknown) => (notifyError(e), null));
        onSaved(link ? { username, link: link.link } : undefined);
      } else {
        await api.updateAccount(account.username, body);
        onSaved();
      }
    } catch (e) {
      setError(errorMessage(e));
      setBusy(false);
    }
  };

  const names = (environments ?? []).map((c) => c.name);
  // Keep names the account has even if this list hasn't loaded them.
  for (const g of picked) if (!names.includes(g.environment)) names.push(g.environment);
  names.sort();

  return (
    <Dialog
      title={isNew ? "New account" : `Edit ${account.username}`}
      onClose={onClose}
      onSubmit={() => void save()}
      footer={
        <>
          <Button variant="quiet" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" variant="primary" disabled={busy || !ready}>
            {isNew ? "Create account" : "Save"}
          </Button>
        </>
      }
    >
      {isNew && (
        <Field label="Username" htmlFor="acc-user" hint={badName ? <span class="text-danger">{usernameHint}</span> : undefined}>
          <input
            id="acc-user"
            type="text"
            autocomplete="off"
            autocapitalize="none"
            spellcheck={false}
            value={username}
            onInput={(e) => setUsername(e.currentTarget.value.toLowerCase())}
          />
        </Field>
      )}
      {isNew && (
        <Field
          label="Signs in with"
          hint={kind === "token" ? `No password: ${who} signs in with a login link, which works once. You get the first one next.` : undefined}
        >
          <Seg label="Signs in with" options={kinds} value={kind} onChange={setKind} />
        </Field>
      )}
      {kind === "password" && (
        <Field
          label={isNew ? "Password" : "New password"}
          htmlFor="acc-pw"
          hint={isNew ? `Give it to ${who}; they can change it after signing in.` : `Setting a password signs ${who} out everywhere.`}
        >
          <input
            id="acc-pw"
            type="password"
            autocomplete="new-password"
            placeholder={isNew ? undefined : "Leave empty to keep the current one"}
            value={password}
            onInput={(e) => setPassword(e.currentTarget.value)}
          />
        </Field>
      )}
      <Field
        label="Description"
        htmlFor="acc-desc"
        hint={description.length > 200 ? <span class="text-danger">At most 200 characters.</span> : "Optional: who or what it is for."}
      >
        <input id="acc-desc" type="text" autocomplete="off" value={description} onInput={(e) => setDescription(e.currentTarget.value)} />
      </Field>
      {share ? (
        <p class="mb-[18px] text-sm text-muted">
          A share: {levelLabel(account?.grants[0]?.level ?? "full").toLowerCase()} of {account?.grants[0]?.environment ?? "its environment"}, by{" "}
          {share.username}. Its access is set when it is shared.
        </p>
      ) : (
        <>
          <Check id="acc-admin" checked={admin} onChange={setAdmin} title="Admin">
            Can use every environment, create and change environments, and manage accounts and images.
          </Check>
          {!admin && (
            <Field
              label={`Environments ${who} can use`}
              hint={isNew ? "Observe only is watching the desktop, nothing else." : `Taking an environment away, or lowering it to observe only, closes ${who}'s open desktop and connections on it.`}
            >
              <LevelPicks
                names={names}
                value={picked.map((g) => ({ name: g.environment, level: g.level }))}
                onChange={(picks) => setPicked(picks.map((p) => ({ environment: p.name, level: p.level })))}
                empty="There are no environments yet."
              />
            </Field>
          )}
        </>
      )}
      {canExpire &&
        (changeExpiry ? (
          <TimeLimitField
            label="Account ends"
            limit={limit}
            date={date}
            onLimit={setLimit}
            onDate={setDate}
            none="It stays until it is deleted."
            ends={`Then ${who} is deleted, and signed out everywhere.`}
          />
        ) : (
          <Field label="Account ends">
            <span class="text-sm">
              <Expiry expires={account?.expires ?? null} />{" "}
              <button type="button" class="cursor-pointer border-0 bg-transparent p-0 font-[inherit] text-accent hover:underline" onClick={() => setChangeExpiry(true)}>
                Change
              </button>
            </span>
          </Field>
        ))}
      <ErrorText error={error} />
    </Dialog>
  );
}
