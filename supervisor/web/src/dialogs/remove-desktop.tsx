import { useEffect, useState } from "preact/hooks";
import { api, type Desktop, type FolderSize } from "../api.ts";
import { bytes } from "../lib/format.ts";
import { Button, Check, Dialog, ErrorText, errorMessage } from "../components/ui.tsx";

export function RemoveDialog({ desktop: c, onClose, onRemoved }: { desktop: Desktop; onClose: () => void; onRemoved?: () => void }) {
  const [files, setFiles] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [size, setSize] = useState<FolderSize | null>(null);

  useEffect(() => {
    api.folder(c.name).then(setSize, () => undefined);
  }, [c.name]);

  const submit = async () => {
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
          The desktop is stopped and removed. {files ? "Its files in " : "Its files stay in "}
          <span class="mono">{c.folder}</span>
          {size && ` (${size.complete ? "" : "at least "}${bytes(size.bytes)})`}
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
          <Button type="submit" variant="danger" disabled={busy}>
            {files ? `Remove ${c.name} and its files` : `Remove ${c.name}`}
          </Button>
        </>
      }
    >
      <Check id="rm-files" checked={files} onChange={setFiles} title="Also delete its files">
        Everything installed and saved in the desktop, including browser logins. This can't be undone.
      </Check>
      <ErrorText error={error} />
    </Dialog>
  );
}
