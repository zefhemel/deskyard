import { api } from "../api.ts";
import { useAction } from "../components/use-action.ts";
import { Button, Dialog, ErrorText } from "../components/ui.tsx";

/** Ends the account's sessions in every other browser; this one is re-issued. */
export function SignOutEverywhereDialog({ onClose }: { onClose: () => void }) {
  const { run, busy, error } = useAction(() => api.signOutEverywhere(), { success: "Signed out everywhere else", onDone: onClose });
  return (
    <Dialog
      title="Sign out everywhere?"
      intro="Every other browser signed in as you is signed out, and its desktops and terminals close. This one stays signed in."
      onClose={onClose}
      onSubmit={() => void run()}
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
