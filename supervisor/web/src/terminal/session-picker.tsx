import { createPortal } from "preact/compat";
import { useEffect, useMemo, useRef, useState } from "preact/hooks";
import { api } from "../api.ts";
import { SessionDiscovery, type SessionListState } from "./sessions.ts";

export function SessionPicker({name, session, onSelect}: {
  name: string; session: string; onSelect: (session: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const [visible, setVisible] = useState(!document.hidden);
  const [draft, setDraft] = useState("");
  const [state, setState] = useState<SessionListState>({names: [], loading: false, error: ""});
  const trigger = useRef<HTMLButtonElement>(null);
  const discovery = useMemo(() => new SessionDiscovery(
    async signal => (await api.terminalSessions(name, signal)).sessions.map(s => s.name), setState,
  ), [name]);
  useEffect(() => {
    const change = () => setVisible(!document.hidden);
    document.addEventListener("visibilitychange", change);
    return () => document.removeEventListener("visibilitychange", change);
  }, []);
  useEffect(() => {
    if (open && visible) discovery.start();
    return () => discovery.stop();
  }, [discovery, open, visible, session]);
  const close = () => setOpen(false);
  const select = (value: string) => {
    close();
    onSelect(value);
    setDraft("");
  };
  const existing = state.names.includes(draft);
  const valid = /^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/.test(draft);
  return <div class="terminal-session-picker">
    <button ref={trigger} type="button" aria-expanded={open} aria-controls="terminal-sessions" onClick={() => setOpen(!open)}>Sessions: {session}</button>
    {open && trigger.current && createPortal(<section id="terminal-sessions" class="terminal-session-panel" aria-label="Terminal sessions" onKeyDown={e => {
      if (e.key === "Escape") { e.stopPropagation(); close(); trigger.current?.focus(); }
    }}>
      <div class="terminal-session-actions">
        <strong>Running sessions</strong>
        <button type="button" aria-label="Refresh sessions" disabled={state.loading} onClick={() => void discovery.refresh()}>Refresh</button>
        <button type="button" aria-label="Close sessions" onClick={close}>Close</button>
      </div>
      <div class="terminal-session-feedback" role="status" aria-live="polite">
        {state.loading && "Refreshing sessions…"}
        {!state.loading && !state.error && !state.names.length && "No running sessions."}
      </div>
      {state.error && <div role="alert" class="terminal-session-error">{state.error} <button type="button" aria-label="Retry sessions" onClick={() => void discovery.refresh()}>Retry</button></div>}
      <ul class="terminal-session-list" aria-label="Running terminal sessions" aria-busy={state.loading}>
        {state.names.map(value => <li key={value}><button type="button" aria-label={`Join ${value}`} aria-current={value === session ? "true" : undefined} onClick={() => select(value)}>
          <span>{value}</span>{value === session && <span>Current</span>}
        </button></li>)}
      </ul>
      <form class="terminal-session-create" onSubmit={e => {
        e.preventDefault();
        if (valid && !existing && !state.loading) select(draft);
      }}>
        <label for="terminal-session">Create another shell</label>
        <div><input id="terminal-session" aria-label="New session name" placeholder="New session name" value={draft} onInput={e => setDraft(e.currentTarget.value)} pattern="[A-Za-z0-9][A-Za-z0-9_-]{0,63}" maxLength={64} required autoCapitalize="off" autoComplete="off" spellcheck={false}/>
          <button type="submit" aria-label="Create session" disabled={!valid || existing || state.loading}>Create</button></div>
        <small>{existing ? "Already running — choose it above to join." : "1–64 letters, digits, _ or -; start with a letter or digit."}</small>
      </form>
    </section>, trigger.current.closest("[data-terminal-page]")!)}
  </div>;
}
