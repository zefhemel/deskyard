import { useState } from "preact/hooks";
import { api, type Desktop } from "../api.ts";
import { Button, Check, Dialog, ErrorText, errorMessage, Field } from "../components/ui.tsx";

export function RemoveDialog({ desktop: c, onClose, onRemoved }: { desktop: Desktop; onClose: () => void; onRemoved?: () => void }) {
  const [files, setFiles] = useState(false);
  const [confirm, setConfirm] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const ready = !files || confirm === c.name;

  const submit = async () => {
    if (!ready) return;
    setBusy(true);
    setError(null);
    try {
      await api.removeDesktop(c.name, files);
      onClose();
      onRemoved?.();
    } catch (e) {
      setError(errorMessage(e));
      setBusy(false);
    }
  };

  return (
    <Dialog
      title={`Remove ${c.name}?`}
      intro={
        <>
          The desktop is stopped and removed, with its cache. {files ? "Its files in " : "Its files stay in "}
          <span class="mono">{c.folder}</span>
          {files ? " are deleted too." : "."}
        </>
      }
      onClose={onClose}
      onSubmit={submit}
      footer={
        <>
          <Button variant="quiet" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" variant="danger" disabled={!ready || busy}>
            {files ? `Remove ${c.name} and its files` : `Remove ${c.name}`}
          </Button>
        </>
      }
    >
      <Check id="rm-files" checked={files} onChange={setFiles} title="Also delete its files">
        Everything installed and saved in the desktop, including browser logins. This can't be undone.
      </Check>
      {files && (
        <Field
          label={
            <>
              Type <span class="mono">{c.name}</span> to confirm
            </>
          }
          htmlFor="rm-confirm"
        >
          <input id="rm-confirm" type="text" autocomplete="off" spellcheck={false} value={confirm} onInput={(e) => setConfirm(e.currentTarget.value)} />
        </Field>
      )}
      <ErrorText error={error} />
    </Dialog>
  );
}
