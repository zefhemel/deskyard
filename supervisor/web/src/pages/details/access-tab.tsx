import { useEffect, useState } from "preact/hooks";
import { api, type AccessGrant, type Computer } from "../../api.ts";
import { AccessPicks } from "../../components/computer-fields.tsx";
import { notify } from "../../components/toast.tsx";
import { Button, ErrorText, errorMessage } from "../../components/ui.tsx";
import { useFetch } from "../../lib/use-fetch.ts";

const key = (grants: AccessGrant[]) =>
  grants
    .map((g) => `${g.username}:${g.level}`)
    .sort()
    .join(",");
const same = (a: AccessGrant[], b: AccessGrant[]) => key(a) === key(b);

export function AccessTab({ computer: c }: { computer: Computer }) {
  const accounts = useFetch(() => api.accounts(), "accounts");
  const [value, setValue] = useState(c.access);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  // Follow changes made elsewhere while nothing is being edited here.
  const [saved, setSaved] = useState(c.access);
  useEffect(() => {
    if (same(value, saved)) setValue(c.access);
    setSaved(c.access);
  }, [key(c.access)]);

  const save = async () => {
    setBusy(true);
    setError(null);
    try {
      await api.setAccess(c.name, value);
      setSaved(value);
      notify(`Saved who can use ${c.name}`);
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  const removed = saved.filter((g) => !value.some((v) => v.username === g.username)).map((g) => g.username);
  const lowered = saved
    .filter((g) => g.level === "full" && value.some((v) => v.username === g.username && v.level === "observe"))
    .map((g) => g.username);
  return (
    <div class="max-w-[620px] px-5 py-5">
      <h2 class="mt-0 mb-1 text-base font-semibold">Accounts</h2>
      <p class="mt-0 mb-3.5 text-sm text-muted">
        Who can open {c.name}. Full access is everything: its desktop, terminals and files, starting and stopping it, the CLI and sharing it.
        Observe only is watching its desktop, nothing else. Changing this doesn't restart anything.
      </p>
      {accounts.data ? <AccessPicks accounts={accounts.data} value={value} onChange={setValue} /> : <ErrorText error={accounts.error} />}
      {removed.length > 0 && (
        <p class="mt-1.5 mb-0 text-[13px] text-muted">
          Taking {c.name} away from {removed.join(", ")} also closes their open desktop, terminals and CLI connections on it, and ends the shares they
          made of it.
        </p>
      )}
      {lowered.length > 0 && (
        <p class="mt-1.5 mb-0 text-[13px] text-muted">
          Lowering {lowered.join(", ")} to observe only closes their open desktop, terminals and CLI connections on it (the desktop reconnects, view
          only), and ends the shares they made of it.
        </p>
      )}
      <div class="mt-4">
        <ErrorText error={error} />
        <Button variant="primary" disabled={busy || same(value, saved)} onClick={save}>
          Save
        </Button>
      </div>
    </div>
  );
}
