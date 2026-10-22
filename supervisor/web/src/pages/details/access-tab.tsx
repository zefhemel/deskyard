import { useEffect, useState } from "preact/hooks";
import { api, type Desktop } from "../../api.ts";
import { AccessPicks } from "../../components/desktop-fields.tsx";
import { notify } from "../../components/toast.tsx";
import { SecretAccessDialog } from "../../components/secret-access.tsx";
import { Button, ErrorText, errorMessage } from "../../components/ui.tsx";
import { useFetch } from "../../lib/use-fetch.ts";

const same = (a: string[], b: string[]) => a.length === b.length && a.every((x) => b.includes(x));

export function AccessTab({ desktop: c }: { desktop: Desktop }) {
  const accounts = useFetch(() => api.accounts(), "accounts");
  const [value, setValue] = useState(c.access);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [confirming, setConfirming] = useState(false);

  // Follow changes made elsewhere while nothing is being edited here.
  const [saved, setSaved] = useState(c.access);
  useEffect(() => {
    if (same(value, saved)) setValue(c.access);
    setSaved(c.access);
  }, [c.access.join(",")]);

  const added = value.filter((u) => !saved.includes(u));
  // Accounts given access to a desktop on an image with secrets use whoever made the image's sign-ins: ask first.
  const askFirst = c.imageSecrets && added.length > 0;
  const save = async () => {
    setConfirming(false);
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
        Who can open {c.name}, start and stop it, and give agents tokens for it. Changing this doesn't restart anything.
      </p>
      {accounts.data ? <AccessPicks accounts={accounts.data} value={value} onChange={setValue} /> : <ErrorText error={accounts.error} />}
      {removed.length > 0 && (
        <p class="mt-1.5 mb-0 text-[13px] text-muted">
          Taking {c.name} away from {removed.join(", ")} also closes their open desktop on it and revokes their agent tokens for it.
        </p>
      )}
      <div class="mt-4">
        <ErrorText error={error} />
        <Button variant="primary" disabled={busy || same(value, saved)} onClick={askFirst ? () => setConfirming(true) : save}>
          Save
        </Button>
      </div>
      {confirming && (
        <SecretAccessDialog
          who={added}
          desktops={[{ name: c.name, image: c.imageTitle || c.image }]}
          onCancel={() => setConfirming(false)}
          onConfirm={() => void save()}
        />
      )}
    </div>
  );
}
