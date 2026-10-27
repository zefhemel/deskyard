import { useState } from "preact/hooks";
import { api, type Remote, type RemoteType } from "../../api.ts";
import { notify } from "../../components/toast.tsx";
import { Button, Dialog, ErrorText, Field, Note, Pick, Picks, Tag, errorMessage } from "../../components/ui.tsx";
import { day } from "../../lib/format.ts";
import { blankDraft, draftFromRemote, fieldsFor, REMOTE_TYPES, remoteDraftErrors, toRemoteInput, type RemoteDraft } from "../../lib/remote-form.ts";
import { useFetch } from "../../lib/use-fetch.ts";

const typeLabel = (t: RemoteType) => REMOTE_TYPES.find((x) => x.type === t)?.label ?? t;

function where(r: Remote): string {
  switch (r.type) {
    case "sftp":
      return `${r.user}@${r.host}${r.port ? `:${r.port}` : ""}:${r.root ?? ""}`;
    case "webdav":
      return `${r.url ?? ""}${r.root ? ` · ${r.root}` : ""}`;
    case "s3":
      return `${r.provider}${r.endpoint ? ` (${r.endpoint})` : ""} · ${r.root ?? ""}`;
    default:
      return r.root ?? "";
  }
}

export function RemotesTab() {
  const list = useFetch(() => api.remotes(), "remotes");
  const [editing, setEditing] = useState<Remote | "new" | null>(null);
  const [removing, setRemoving] = useState<Remote | null>(null);
  const remotes = list.data?.remotes;
  return (
    <>
      <div class="mb-4 flex flex-wrap items-center gap-3">
        <p class="mr-auto max-w-[640px] text-sm text-muted">
          Storage remotes are connections to storage elsewhere: a bucket, a NAS or a WebDAV server. Backup repositories live on them.
        </p>
        <Button variant="primary" onClick={() => setEditing("new")}>
          Add remote
        </Button>
      </div>
      <ErrorText error={list.error} />
      {!remotes ? (
        !list.error && <p class="text-muted">Loading…</p>
      ) : !remotes.length ? (
        <p class="text-muted">No remotes yet. Add one to back up to it.</p>
      ) : (
        <div class="grid gap-3">
          {remotes.map((r) => (
            <article key={r.name} class="rounded-[10px] border border-line bg-surface p-5">
              <div class="flex flex-wrap items-start gap-x-4 gap-y-2">
                <div class="mr-auto min-w-0">
                  <h2 class="text-lg font-semibold">
                    {r.name} <Tag>{typeLabel(r.type)}</Tag>
                  </h2>
                  <p class="mono text-sm break-all text-muted">{where(r)}</p>
                </div>
                <Button onClick={() => setEditing(r)}>Edit</Button>
                <Button onClick={() => setRemoving(r)}>Delete</Button>
              </div>
              <p class="mt-3 text-sm text-muted">
                Added {day(r.created)}.{" "}
                {r.repositories.length ? `Backup repositories: ${r.repositories.join(", ")}.` : "No backup repository on it yet."}
              </p>
            </article>
          ))}
        </div>
      )}
      {editing && <RemoteDialog remote={editing === "new" ? null : editing} onClose={() => setEditing(null)} onSaved={list.reload} />}
      {removing && <DeleteRemote remote={removing} onClose={() => setRemoving(null)} onDeleted={list.reload} />}
    </>
  );
}

function RemoteDialog({ remote, onClose, onSaved }: { remote: Remote | null; onClose: () => void; onSaved: () => void }) {
  const creating = remote === null;
  const [d, setD] = useState<RemoteDraft>(remote ? draftFromRemote(remote) : blankDraft("s3"));
  const [busy, setBusy] = useState<"" | "test" | "save">("");
  const [error, setError] = useState<string | null>(null);
  const [tested, setTested] = useState(false);
  const errs = remoteDraftErrors(d, creating, remote?.secretsSet ?? []);
  const valid = Object.keys(errs).length === 0;
  const set = (change: Partial<RemoteDraft>) => {
    setD({ ...d, ...change });
    setTested(false);
  };
  const moved = remote && (d.host !== (remote.host ?? "") || d.root !== (remote.root ?? "") || d.url !== (remote.url ?? "") || d.endpoint !== (remote.endpoint ?? ""));
  const test = async () => {
    setBusy("test");
    setError(null);
    try {
      const { remote: r, secrets } = toRemoteInput(d);
      await api.testRemote(r, secrets, remote?.name);
      setTested(true);
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy("");
    }
  };
  const save = async () => {
    setBusy("save");
    setError(null);
    try {
      const { remote: r, secrets } = toRemoteInput(d);
      if (creating) await api.createRemote(r, secrets);
      else await api.updateRemote(remote.name, r, secrets);
      notify(creating ? `Added remote ${d.name}` : `Saved remote ${d.name}`);
      onSaved();
      onClose();
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy("");
    }
  };
  const shown = (k: string) => (d as unknown as Record<string, string>)[k] !== "" || !creating;
  return (
    <Dialog
      title={creating ? "Add remote" : `Edit ${remote.name}`}
      intro="Deskyard connects to it before saving."
      wide
      onClose={onClose}
      onSubmit={() => valid && void save()}
      footer={
        <>
          <Button class="mr-auto" disabled={!valid || !!busy} onClick={() => void test()}>
            {busy === "test" ? "Connecting…" : tested ? "Connected ✓" : "Test connection"}
          </Button>
          <Button variant="quiet" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" variant="primary" disabled={!valid || !!busy}>
            {busy === "save" ? "Connecting…" : creating ? "Add remote" : "Save"}
          </Button>
        </>
      }
    >
      {creating && (
        <Field label="Type">
          <Picks>
            {REMOTE_TYPES.map((t) => (
              <Pick key={t.type} checked={d.type === t.type} onChange={() => setD({ ...blankDraft(t.type), name: d.name })}>
                <span>
                  <b class="font-semibold">{t.label}</b> <span class="text-muted">· {t.hint}</span>
                </span>
              </Pick>
            ))}
          </Picks>
        </Field>
      )}
      <Field label="Name" htmlFor="remote-name" hint={d.name && errs.name ? <span class="text-danger">{errs.name}</span> : creating ? "It can't be changed later." : undefined}>
        <input id="remote-name" value={d.name} disabled={!creating} autocomplete="off" spellcheck={false} onInput={(e) => set({ name: e.currentTarget.value.toLowerCase() })} />
      </Field>
      {fieldsFor(d.type).map((f) => {
        const id = `remote-${f.key}`;
        const value = d[f.key];
        const err = errs[f.key] && (shown(f.key) || f.secret) ? errs[f.key] : undefined;
        const keep = !creating && f.secret && remote.secretsSet.includes(f.key);
        const hint = err ? <span class="text-danger">{err}</span> : keep ? "Saved. Leave empty to keep it." : f.hint;
        return (
          <Field key={f.key} label={f.label} htmlFor={id} hint={hint}>
            {f.options ? (
              <select id={id} value={value} onChange={(e) => set({ [f.key]: e.currentTarget.value })}>
                {f.options.map((o) => (
                  <option key={o} value={o}>
                    {o}
                  </option>
                ))}
              </select>
            ) : f.multiline ? (
              <textarea id={id} class="mono min-h-[90px] w-full" value={value} placeholder={keep ? "••••••" : f.placeholder} spellcheck={false} onInput={(e) => set({ [f.key]: e.currentTarget.value })} />
            ) : f.secret ? (
              <input id={id} type="password" value={value} placeholder={keep ? "••••••" : f.placeholder} autocomplete="new-password" onInput={(e) => set({ [f.key]: e.currentTarget.value })} />
            ) : (
              <input id={id} type="text" class="mono" value={value} placeholder={f.placeholder} autocomplete="off" spellcheck={false} onInput={(e) => set({ [f.key]: e.currentTarget.value })} />
            )}
          </Field>
        );
      })}
      {moved && remote.repositories.length > 0 && (
        <Note>
          {remote.repositories.join(", ")} {remote.repositories.length === 1 ? "is" : "are"} on this remote. Pointing it somewhere else makes them unreachable until it points at their folder again.
        </Note>
      )}
      <ErrorText error={error} />
    </Dialog>
  );
}

function DeleteRemote({ remote: r, onClose, onDeleted }: { remote: Remote; onClose: () => void; onDeleted: () => void }) {
  const [error, setError] = useState<string | null>(null);
  const remove = async () => {
    try {
      await api.deleteRemote(r.name);
      notify(`Deleted remote ${r.name}`);
      onDeleted();
      onClose();
    } catch (e) {
      setError(errorMessage(e));
    }
  };
  return (
    <Dialog
      title={`Delete ${r.name}?`}
      onClose={onClose}
      onSubmit={() => void remove()}
      footer={
        <>
          <Button variant="quiet" onClick={onClose}>
            Close
          </Button>
          <Button type="submit" variant="danger" disabled={r.repositories.length > 0}>
            Delete remote
          </Button>
        </>
      }
    >
      {r.repositories.length ? (
        <p class="text-sm">Remove its backup repositories first: {r.repositories.join(", ")}.</p>
      ) : (
        <p class="text-sm">Deskyard forgets the connection and its credentials. Nothing stored on the remote is touched.</p>
      )}
      <ErrorText error={error} />
    </Dialog>
  );
}
