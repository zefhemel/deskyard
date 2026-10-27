import { useState } from "preact/hooks";
import { api } from "../api.ts";
import { notify } from "../components/toast.tsx";
import { Button, Dialog, ErrorText, errorMessage } from "../components/ui.tsx";

/** Ends the account's sessions in every other browser; this one is re-issued. */
export function SignOutEverywhereDialog({ onClose }: { onClose: () => void }) {
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const submit = async () => {
    setBusy(true);
    try {
      await api.signOutEverywhere();
      notify("Signed out everywhere else");
      onClose();
    } catch (e) {
      setError(errorMessage(e));
      setBusy(false);
    }
  };
  return (
    <Dialog
      title="Sign out everywhere?"
      intro="Every other browser signed in as you is signed out, and its desktops and terminals close. This one stays signed in."
      onClose={onClose}
      onSubmit={() => void submit()}
      footer={
        <>
          <Button variant="quiet" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" variant="danger" disabled={busy}>
            Sign out everywhere
          </Button>
        </>
      }
    >
      <ErrorText error={error} />
    </Dialog>
  );
}
