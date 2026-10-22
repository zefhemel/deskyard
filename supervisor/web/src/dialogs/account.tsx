// New account and Edit account (password, admin, computers).
import { useState } from "preact/hooks";
import { api, type Account } from "../api.ts";
import { Button, Check, Dialog, ErrorText, errorMessage, Field, Pick, Picks } from "../components/ui.tsx";
import { useLive } from "../live.tsx";

const USERNAME = /^[a-z][a-z0-9._-]{0,31}$/;

export function AccountDialog({ account, onClose, onSaved }: { account: Account | null; onClose: () => void; onSaved: () => void }) {
  const { computers } = useLive();
  const isNew = account === null;
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [admin, setAdmin] = useState(account?.admin ?? false);
  const [picked, setPicked] = useState<string[]>(account?.computers ?? []);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const who = isNew ? username || "they" : account.username;

  const badName = isNew && username !== "" && !USERNAME.test(username);
  const ready = isNew ? USERNAME.test(username) && password !== "" : true;

  const submit = async () => {
    if (!ready) return;
    setBusy(true);
    setError(null);
    try {
      if (isNew) {
        await api.createAccount({ username, password, admin, computers: admin ? [] : picked });
      } else {
        const body: { password?: string; admin?: boolean; computers?: string[] } = { computers: picked };
        if (password) body.password = password;
        if (admin !== account.admin) body.admin = admin;
        await api.updateAccount(account.username, body);
      }
      onSaved();
    } catch (e) {
      setError(errorMessage(e));
      setBusy(false);
    }
  };

  const names = (computers ?? []).map((c) => c.name);
  // Keep names the account has even if this list hasn't loaded them.
  for (const n of picked) if (!names.includes(n)) names.push(n);
  names.sort();

  return (
    <Dialog
      title={isNew ? "New account" : `Edit ${account.username}`}
      onClose={onClose}
      onSubmit={submit}
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
        <Field
          label="Username"
          htmlFor="acc-user"
          hint={badName ? <span class="text-danger">Lowercase letters, digits, dots, dashes and underscores, starting with a letter.</span> : undefined}
        >
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
      <Check id="acc-admin" checked={admin} onChange={setAdmin} title="Admin">
        Can use every computer, create and change computers, and manage accounts and images.
      </Check>
      {!admin && (
        <Field label={`Computers ${who} can use`} hint={isNew ? undefined : `Taking a computer away also closes ${who}'s open desktop on it.`}>
          <Picks>
            {names.map((n) => (
              <Pick key={n} checked={picked.includes(n)} onChange={(on) => setPicked(on ? [...picked, n] : picked.filter((x) => x !== n))}>
                {n}
              </Pick>
            ))}
            {!names.length && <div class="px-3 py-2 text-sm text-muted">There are no computers yet.</div>}
          </Picks>
        </Field>
      )}
      <ErrorText error={error} />
    </Dialog>
  );
}
