import { useState } from "preact/hooks";
import { api } from "../api.ts";
import { Button, Dialog, ErrorText, errorMessage, Field } from "../components/ui.tsx";

export function ChangePasswordDialog({ onClose }: { onClose: () => void }) {
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [again, setAgain] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [done, setDone] = useState(false);

  const submit = async () => {
    if (next !== again) {
      setError("The new passwords don't match.");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await api.changePassword(current, next);
      setDone(true);
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  if (done) {
    return (
      <Dialog title="Password changed" intro="You're signed out everywhere else." onClose={onClose} onSubmit={onClose} footer={<Button type="submit" variant="primary">Done</Button>} />
    );
  }

  return (
    <Dialog
      title="Change password"
      intro="Changing it signs you out everywhere else."
      onClose={onClose}
      onSubmit={submit}
      footer={
        <>
          <Button variant="quiet" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" variant="primary" disabled={busy || !current || !next}>
            Change password
          </Button>
        </>
      }
    >
      {/* A hidden username helps password managers file the new password. */}
      <input type="text" autocomplete="username" class="hidden" aria-hidden="true" tabIndex={-1} />
      <Field label="Current password" htmlFor="pw-current">
        <input id="pw-current" type="password" autocomplete="current-password" value={current} onInput={(e) => setCurrent(e.currentTarget.value)} />
      </Field>
      <Field label="New password" htmlFor="pw-new">
        <input id="pw-new" type="password" autocomplete="new-password" value={next} onInput={(e) => setNext(e.currentTarget.value)} />
      </Field>
      <Field label="New password again" htmlFor="pw-again">
        <input id="pw-again" type="password" autocomplete="new-password" value={again} onInput={(e) => setAgain(e.currentTarget.value)} />
      </Field>
      <ErrorText error={error} />
    </Dialog>
  );
}
