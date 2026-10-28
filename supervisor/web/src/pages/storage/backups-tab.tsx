import { useState } from "preact/hooks";
import { api, ApiError, type BackupsOverview, type CatalogItem, type Repository } from "../../api.ts";
import { RestoreNewDialog } from "../../components/backups/restore-dialogs.tsx";
import { SecretReveal } from "../../components/secret-reveal.tsx";
import { notify } from "../../components/toast.tsx";
import { Button, Dialog, ErrorText, Field, Note, Tag, errorMessage } from "../../components/ui.tsx";
import { backupStatus, describeSchedule, isTime, itemCopy, splitItem } from "../../lib/backup-policy.ts";
import { ago, day } from "../../lib/format.ts";
import { RESOURCE_NAME, resourceNameHint } from "../../lib/names.ts";
import { useBackups, useFetch } from "../../lib/use-fetch.ts";

function itemHref(item: string): string {
  return itemCopy(...splitItem(item)).page;
}

export function BackupsTab() {
  const overview = useBackups();
  const remotes = useFetch(() => api.remotes(), "remotes");
  const [creating, setCreating] = useState(false);
  const [catalog, setCatalog] = useState<BackupsOverview | null>(null);
  const [looking, setLooking] = useState(false);
  const [lookError, setLookError] = useState<string | null>(null);
  const look = async () => {
    setLooking(true);
    setLookError(null);
    try {
      setCatalog(await api.backups(true));
    } catch (e) {
      setLookError(errorMessage(e));
    } finally {
      setLooking(false);
    }
  };
  const o = overview.data;
  return (
    <>
      <section aria-label="Backup repositories">
        <div class="mb-4 flex flex-wrap items-center gap-3">
          <h2 class="mr-auto text-lg font-semibold">Repositories</h2>
          <Button variant="primary" disabled={!remotes.data?.remotes.length} onClick={() => setCreating(true)}>
            Add repository
          </Button>
        </div>
        {remotes.data && !remotes.data.remotes.length && (
          <p class="mb-4 text-sm text-muted">
            Add a <a href="/volumes?tab=remotes">remote</a> first.
          </p>
        )}
        <ErrorText error={overview.error} />
        {o && !o.repositories.length && <p class="text-sm text-muted">No repositories yet. A repository is a folder on a remote that holds encrypted snapshots.</p>}
        <div class="grid gap-3">
          {o?.repositories.map((r) => (
            <RepositoryCard key={r.name} repository={r} used={o.items.filter((i) => i.policy?.repository === r.name).map((i) => splitItem(i.item)[1])} onRemoved={overview.reload} />
          ))}
        </div>
      </section>

      <section aria-label="Backed up" class="mt-8">
        <h2 class="mb-2 text-lg font-semibold">Backed up</h2>
        {o && !o.items.length && <p class="text-sm text-muted">Nothing yet. Set up backups on an environment's Backups tab or a volume's page.</p>}
        {!!o?.items.length && (
          <ul class="m-0 list-none divide-y divide-hairline rounded-[10px] border border-line bg-surface p-0">
            {o.items.map((i) => (
              <li key={i.item} class="flex flex-wrap items-center gap-x-4 gap-y-1 px-4 py-3">
                <span class="mr-auto min-w-0">
                  <a href={itemHref(i.item)} class="font-semibold">
                    {splitItem(i.item)[1]}
                  </a>{" "}
                  <span class="text-sm text-muted">{splitItem(i.item)[0] === "environment" ? "environment" : "volume"}</span>
                </span>
                {i.policy && <span class="text-sm text-muted">{i.policy.enabled ? describeSchedule(i.policy.schedule) : "Paused"} · {i.policy.repository}</span>}
                <span class="text-sm">{isTime(i.state.lastSuccess) ? ago(i.state.lastSuccess) : "Never"}</span>
                {(() => {
                  const st = backupStatus(i);
                  return st && <Tag kind={st.kind}>{st.label}</Tag>;
                })()}
                {i.state.error && (
                  <span class="w-full text-sm text-danger" role="alert">
                    Failed: {i.state.error}
                  </span>
                )}
              </li>
            ))}
          </ul>
        )}
      </section>

      <section aria-label="Other backups" class="mt-8">
        <div class="mb-2 flex flex-wrap items-center gap-3">
          <div class="mr-auto">
            <h2 class="text-lg font-semibold">Other backups</h2>
            <p class="text-sm text-muted">Of removed environments and volumes, and other machines.</p>
          </div>
          <Button disabled={looking || !o?.repositories.length} onClick={() => void look()}>
            {looking ? "Looking…" : catalog ? "Refresh" : "Show"}
          </Button>
        </div>
        <ErrorText error={lookError} />
        {catalog && <Catalog overview={catalog} />}
      </section>
      {creating && remotes.data && <CreateRepository remotes={remotes.data.remotes.map((r) => r.name)} onClose={() => setCreating(false)} onCreated={overview.reload} />}
    </>
  );
}

function Catalog({ overview: o }: { overview: BackupsOverview }) {
  const [restoring, setRestoring] = useState<CatalogItem | null>(null);
  const items = (o.catalog ?? []).filter((c) => !c.local);
  return (
    <>
      {Object.entries(o.catalogErrors ?? {}).map(([repo, err]) => (
        <ErrorText key={repo} error={`${repo}: ${err}`} />
      ))}
      {!items.length ? (
        <p class="text-sm text-muted">None.</p>
      ) : (
        <ul class="m-0 list-none divide-y divide-hairline rounded-[10px] border border-line bg-surface p-0">
          {items.map((c) => (
            <li key={`${c.repository} ${c.host} ${c.item}`} class="flex flex-wrap items-center gap-x-4 gap-y-1 px-4 py-3">
              <div class="mr-auto min-w-0">
                <div>
                  <span class="font-semibold">{splitItem(c.item)[1]}</span> <span class="text-sm text-muted">{splitItem(c.item)[0]}</span>
                </div>
                <div class="text-sm text-muted">
                  {c.snapshots} snapshot{c.snapshots === 1 ? "" : "s"}, latest {day(c.latest)} · {c.repository}
                  {c.host !== o.host && ` · ${c.host}`}
                </div>
              </div>
              <Button onClick={() => setRestoring(c)}>Restore as new</Button>
            </li>
          ))}
        </ul>
      )}
      {restoring && (
        <RestoreNewDialog
          kind={splitItem(restoring.item)[0]}
          name={splitItem(restoring.item)[1]}
          snapshot={{ id: restoring.latestId, time: restoring.latest, repository: restoring.repository }}
          onClose={() => setRestoring(null)}
        />
      )}
    </>
  );
}

function RepositoryCard({ repository: r, used, onRemoved }: { repository: Repository; used: string[]; onRemoved: () => void }) {
  const [key, setKey] = useState<string | null>(null);
  const [removing, setRemoving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const reveal = async () => {
    try {
      setKey((await api.recoveryKey(r.name)).recoveryKey);
    } catch (e) {
      setError(errorMessage(e));
    }
  };
  return (
    <article class="rounded-[10px] border border-line bg-surface p-5">
      <div class="mb-4 flex flex-wrap items-start gap-x-4 gap-y-2">
        <h3 class="mr-auto min-w-0 text-lg font-semibold">{r.name}</h3>
        <Button onClick={() => (key ? setKey(null) : void reveal())}>{key ? "Hide recovery key" : "Recovery key"}</Button>
        <Button onClick={() => setRemoving(true)}>Remove</Button>
      </div>
      <dl class="grid gap-x-6 gap-y-1 text-sm sm:grid-cols-[7.5rem_1fr] sm:gap-y-3">
        <dt class="text-muted">Location</dt>
        <dd class="mono min-w-0 break-all">
          {r.remote}:{r.folder}
        </dd>
        <dt class="mt-2 text-muted sm:mt-0">Backs up</dt>
        <dd class="min-w-0">{used.length ? used.join(", ") : <span class="text-muted">Nothing yet</span>}</dd>
      </dl>
      {key && <KeyPanel recoveryKey={key} />}
      <ErrorText error={error} />
      {removing && <RemoveRepository repository={r} onClose={() => setRemoving(false)} onRemoved={onRemoved} />}
    </article>
  );
}

function KeyPanel({ recoveryKey }: { recoveryKey: string }) {
  return (
    <SecretReveal
      class="mt-3 bg-white"
      secret={recoveryKey}
      label="Recovery key"
      what="the recovery key"
      note="Without this key the backups can't be read. Keep it in a password manager."
    />
  );
}

function CreateRepository({ remotes, onClose, onCreated }: { remotes: string[]; onClose: () => void; onCreated: () => void }) {
  const [name, setName] = useState(remotes.length === 0 ? "" : "backups");
  const [remote, setRemote] = useState(remotes[0] ?? "");
  const [folder, setFolder] = useState("deskyard-backups");
  const [key, setKey] = useState("");
  const [needsKey, setNeedsKey] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [created, setCreated] = useState<{ repository: Repository; recoveryKey?: string } | null>(null);
  const valid = RESOURCE_NAME.test(name) && !!remote && !!folder.trim() && (!needsKey || !!key.trim());
  const submit = async () => {
    setBusy(true);
    setError(null);
    try {
      const res = await api.createRepository({ name, remote, folder: folder.trim(), recoveryKey: needsKey ? key.trim() : undefined });
      onCreated();
      if (res.recoveryKey) setCreated(res);
      else {
        notify(`Connected repository ${name}`);
        onClose();
      }
    } catch (e) {
      if (e instanceof ApiError && e.code === "needsKey") {
        setNeedsKey(true);
        setError("That folder already holds a repository. Enter its recovery key to connect it.");
      } else setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };
  if (created?.recoveryKey) {
    return (
      <Dialog title={`Created ${created.repository.name}`} onClose={onClose} onSubmit={onClose} footer={<Button type="submit" variant="primary">I've saved it</Button>}>
        <p class="text-sm">This is the repository's recovery key.</p>
        <KeyPanel recoveryKey={created.recoveryKey} />
      </Dialog>
    );
  }
  return (
    <Dialog
      title="Add backup repository"
      onClose={onClose}
      onSubmit={() => valid && void submit()}
      footer={
        <>
          <Button variant="quiet" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" variant="primary" disabled={!valid || busy}>
            {busy ? "Connecting…" : needsKey ? "Connect" : "Add repository"}
          </Button>
        </>
      }
    >
      <Field label="Name" htmlFor="repo-name" hint={name && !RESOURCE_NAME.test(name) ? <span class="text-danger">{resourceNameHint}</span> : undefined}>
        <input type="text" id="repo-name" value={name} autocomplete="off" spellcheck={false} onInput={(e) => setName(e.currentTarget.value.toLowerCase())} />
      </Field>
      <Field label="Remote" htmlFor="repo-remote">
        <select id="repo-remote" value={remote} onChange={(e) => setRemote(e.currentTarget.value)}>
          {remotes.map((r) => (
            <option key={r} value={r}>
              {r}
            </option>
          ))}
        </select>
      </Field>
      <Field label="Folder on the remote" htmlFor="repo-folder" hint="Can't be changed later.">
        <input type="text" id="repo-folder" class="mono" value={folder} spellcheck={false} onInput={(e) => { setFolder(e.currentTarget.value); setNeedsKey(false); }} />
      </Field>
      {needsKey && (
        <Field label="Recovery key" htmlFor="repo-key">
          <input id="repo-key" type="password" autocomplete="off" value={key} onInput={(e) => setKey(e.currentTarget.value)} />
        </Field>
      )}
      <ErrorText error={error} />
    </Dialog>
  );
}

function RemoveRepository({ repository: r, onClose, onRemoved }: { repository: Repository; onClose: () => void; onRemoved: () => void }) {
  const [error, setError] = useState<string | null>(null);
  const remove = async () => {
    try {
      await api.removeRepository(r.name);
      notify(`Removed repository ${r.name}`);
      onRemoved();
      onClose();
    } catch (e) {
      setError(errorMessage(e));
    }
  };
  return (
    <Dialog
      title={`Remove ${r.name}?`}
      onClose={onClose}
      onSubmit={() => void remove()}
      footer={
        <>
          <Button variant="quiet" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" variant="danger">
            Remove repository
          </Button>
        </>
      }
    >
      <Note>Its snapshots stay on {r.remote}, but you need the recovery key to add it again. Save the key first.</Note>
      <ErrorText error={error} />
    </Dialog>
  );
}
