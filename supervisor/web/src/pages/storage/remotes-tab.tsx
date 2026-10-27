import { useState } from "preact/hooks";
import { api, type Remote, type RemoteType } from "../../api.ts";
import { notify } from "../../components/toast.tsx";
import { Button, Dialog, ErrorText, Field, Note, Tag, errorMessage } from "../../components/ui.tsx";
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
      <div class="mb-4 flex justify-end">
        <Button variant="primary" onClick={() => setEditing("new")}>
          Add remote
        </Button>
      </div>
      <ErrorText error={list.error} />
      {!remotes ? (
        !list.error && <p class="text-muted">Loading…</p>
      ) : !remotes.length ? (
        <p class="text-muted">No remotes yet. A remote is storage elsewhere, such as a bucket, a NAS or a WebDAV server, to back up to.</p>
      ) : (
        <div class="grid gap-3">
          {remotes.map((r) => (
            <article key={r.name} class="rounded-[10px] border border-line bg-surface p-5">
              <div class="mb-4 flex flex-wrap items-start gap-x-4 gap-y-2">
                <h2 class="mr-auto min-w-0 text-lg font-semibold">
                  {r.name} <Tag>{typeLabel(r.type)}</Tag>
                </h2>
                <Button onClick={() => setEditing(r)}>Edit</Button>
                <Button onClick={() => setRemoving(r)}>Delete</Button>
              </div>
              <dl class="grid gap-x-6 gap-y-1 text-sm sm:grid-cols-[7.5rem_1fr] sm:gap-y-3">
                <dt class="text-muted">Location</dt>
                <dd class="mono min-w-0 break-all">{where(r)}</dd>
                <dt class="mt-2 text-muted sm:mt-0">Repositories</dt>
                <dd class="min-w-0">{r.repositories.length ? r.repositories.join(", ") : <span class="text-muted">None</span>}</dd>
              </dl>
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
  // Errors show for fields typed in, and for all once saving is tried.
  const [touched, setTouched] = useState<Set<string>>(new Set());
  const [tried, setTried] = useState(false);
  const errs = remoteDraftErrors(d, creating, remote?.secretsSet ?? []);
  const valid = Object.keys(errs).length === 0;
  const set = (change: Partial<RemoteDraft>) => {
    setD({ ...d, ...change });
    setTouched(new Set([...touched, ...Object.keys(change)]));
    setTested(false);
  };
  const shows = (key: string) => tried || touched.has(key);
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
  return (
    <Dialog
      title={creating ? "Add remote" : `Edit ${remote.name}`}
      wide
      onClose={onClose}
      onSubmit={() => (valid ? void save() : setTried(true))}
      footer={
        <>
          <Button class="mr-auto" disabled={!valid || !!busy} onClick={() => void test()}>
            {busy === "test" ? "Connecting…" : tested ? "Connected ✓" : "Test connection"}
          </Button>
          <Button variant="quiet" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" variant="primary" disabled={!!busy}>
            {busy === "save" ? "Connecting…" : creating ? "Add remote" : "Save"}
          </Button>
        </>
      }
    >
      {creating && (
        <Field label="Type">
          <div role="radiogroup" aria-label="Type" class="rounded-lg border border-line bg-white">
            {REMOTE_TYPES.map((t) => (
              <label key={t.type} class="flex items-center gap-2.5 border-b border-hairline px-3 py-2 text-sm last:border-b-0">
                <input
                  type="radio"
                  name="remote-type"
                  checked={d.type === t.type}
                  onChange={() => {
                    setD({ ...blankDraft(t.type), name: d.name });
                    setTouched(new Set(d.name ? ["name"] : []));
                  }}
                />
                <span>
                  <b class="font-semibold">{t.label}</b> <span class="text-muted">· {t.hint}</span>
                </span>
              </label>
            ))}
          </div>
        </Field>
      )}
      <Field label="Name" htmlFor="remote-name" hint={shows("name") && errs.name ? <span class="text-danger">{errs.name}</span> : creating ? "Can't be changed later." : undefined}>
        <input type="text" id="remote-name" value={d.name} disabled={!creating} autocomplete="off" spellcheck={false} onInput={(e) => set({ name: e.currentTarget.value.toLowerCase() })} />
      </Field>
      {fieldsFor(d.type).map((f) => {
        const id = `remote-${f.key}`;
        const value = d[f.key];
        const err = shows(f.key) ? errs[f.key] : undefined;
        const keep = !creating && f.secret && remote.secretsSet.includes(f.key);
        const hint = err ? <span class="text-danger">{err}</span> : keep ? "Leave empty to keep the saved one." : f.hint;
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
          Pointing it elsewhere makes {remote.repositories.join(", ")} unreachable.
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
        <p class="text-sm">Remove its repositories first: {r.repositories.join(", ")}.</p>
      ) : (
        <p class="text-sm">Nothing stored on the remote is touched.</p>
      )}
      <ErrorText error={error} />
    </Dialog>
  );
}
