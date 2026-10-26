import { useEffect, useState } from "preact/hooks";
import { api, type Desktop } from "../../api.ts";
import { AccessPicks } from "../../components/desktop-fields.tsx";
import { notify } from "../../components/toast.tsx";
import { Button, ErrorText, errorMessage } from "../../components/ui.tsx";
import { useFetch } from "../../lib/use-fetch.ts";

const same = (a: string[], b: string[]) => a.length === b.length && a.every((x) => b.includes(x));

export function AccessTab({ desktop: c }: { desktop: Desktop }) {
  const accounts = useFetch(() => api.accounts(), "accounts");
  const [value, setValue] = useState(c.access);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  // Follow changes made elsewhere while nothing is being edited here.
  const [saved, setSaved] = useState(c.access);
  useEffect(() => {
    if (same(value, saved)) setValue(c.access);
    setSaved(c.access);
  }, [c.access.join(",")]);

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

  const removed = saved.filter((u) => !value.includes(u));
  return (
    <div class="max-w-[620px] px-5 py-5">
      <p class="mt-0 mb-3.5 text-sm text-muted">
        Who can open {c.name}, start and stop it, and create access tokens for it. Changing this doesn't restart anything.
      </p>
      {accounts.data ? <AccessPicks accounts={accounts.data} value={value} onChange={setValue} /> : <ErrorText error={accounts.error} />}
      {removed.length > 0 && (
        <p class="mt-1.5 mb-0 text-[13px] text-muted">
          Taking {c.name} away from {removed.join(", ")} also closes their open desktop on it and revokes their access tokens for it.
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
