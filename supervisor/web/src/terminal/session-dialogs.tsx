// The terminal tabs' dialogs: ending a session for everyone, and renaming one.
import { useState } from "preact/hooks";
import { api } from "../api.ts";
import { Button, Dialog, ErrorText, Field } from "../components/ui.tsx";
import { isShell, validSession } from "./sessions.ts";

export function EndSessionDialog({ session, others: n, command, onEnd, onClose }: { session: string; others: number; command: string; onEnd: () => void; onClose: () => void }) {
  const program = !isShell(command);
  return (
    <Dialog
      title={`End ${session} for everyone?`}
      onClose={onClose}
      onSubmit={onEnd}
      footer={
        <>
          <Button onClick={onClose}>Cancel</Button>
          <Button type="submit" variant="danger">
            End session
          </Button>
        </>
      }
    >
      <ul class="m-0 list-disc space-y-1 pl-5">
        {program && <li>{command ? <><code>{command}</code> is running in it.</> : "A program may be running in it."}</li>}
        {n > 0 && <li>{n === 1 ? "Someone else is attached" : `${n} others are attached`}: their terminals close too.</li>}
      </ul>
      <p class="mt-2 text-muted">Its shell and every program in it stop.</p>
    </Dialog>
  );
}

export function RenameSessionDialog({ session, environment, onClose, onRenamed, onStart, onFailure }: { session: string; environment: string; onClose: () => void; onRenamed: (next: string) => void; onStart: () => void; onFailure: () => void }) {
  const [name, setName] = useState(session);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const valid = validSession(name);
  return <Dialog title={`Rename ${session}`} onClose={() => { if (!busy) onClose(); }} onSubmit={() => {
    if (busy || !valid) return;
    setBusy(true);
    setError(null);
    onStart();
    api.renameTerminal(environment, session, name).then(({ name }) => onRenamed(name), (e: unknown) => {
      onFailure();
      setError(e instanceof Error ? e.message : String(e)); setBusy(false);
    });
  }} footer={<><Button onClick={onClose} disabled={busy}>Cancel</Button><Button type="submit" disabled={busy || !valid}>{busy ? "Renaming…" : "Rename"}</Button></>}>
    <Field label="Session name" htmlFor="terminal-session-name"><input id="terminal-session-name" autoFocus value={name} maxLength={64} disabled={busy} onInput={(e) => setName(e.currentTarget.value)} /></Field>
    <p class="text-sm text-muted">Use 1–64 letters, digits, underscores or dashes, starting with a letter or digit. The shell and its programs keep running.</p>
    <ErrorText error={error} />
  </Dialog>;
}
