import { useState } from "preact/hooks";
import { api, ApiError, type BackupsOverview, type CatalogItem, type Repository } from "../../api.ts";
import { RestoreNewDialog } from "../../components/backups-panel.tsx";
import { notify } from "../../components/toast.tsx";
import { Button, Dialog, ErrorText, Field, Note, Tag, errorMessage } from "../../components/ui.tsx";
import { describeSchedule, isTime, splitItem } from "../../lib/backup-policy.ts";
import { ago, day } from "../../lib/format.ts";
import { useFetch } from "../../lib/use-fetch.ts";

const NAME = /^[a-z][a-z0-9-]{0,30}[a-z0-9]$/;

function itemHref(item: string): string {
  const [kind, name] = splitItem(item);
  return kind === "environment" ? `/environments/${name}?tab=backups` : `/volumes/${name}`;
}

export function BackupsTab() {
  const overview = useFetch(() => api.backups(), "backups");
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
          <div class="mr-auto max-w-[640px]">
            <h2 class="text-lg font-semibold">Backup repositories</h2>
            <p class="text-sm text-muted">A repository is a folder on a remote where restic keeps encrypted, deduplicated snapshots. Its recovery key opens it from anywhere.</p>
          </div>
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
        {o && !o.repositories.length && <p class="text-sm text-muted">No repositories yet.</p>}
        <div class="grid gap-3">
          {o?.repositories.map((r) => (
            <RepositoryCard key={r.name} repository={r} used={o.items.filter((i) => i.policy?.repository === r.name).length} onRemoved={overview.reload} />
          ))}
        </div>
      </section>

      <section aria-label="Backed up" class="mt-8">
        <h2 class="mb-2 text-lg font-semibold">Backed up</h2>
        {o && !o.items.length && <p class="text-sm text-muted">Nothing is backed up yet. Set up backups on an environment's or a volume's Backups tab.</p>}
        {!!o?.items.length && (
          <ul class="m-0 list-none divide-y divide-hairline rounded-[10px] border border-line bg-surface p-0">
            {o.items.map((i) => (
              <li key={i.item} class="flex flex-wrap items-center gap-x-4 gap-y-1 px-4 py-3">
                <a href={itemHref(i.item)} class="mr-auto font-semibold">
                  {i.item.replace(":", " ")}
                </a>
                {i.policy && <span class="text-sm text-muted">{i.policy.enabled ? describeSchedule(i.policy.schedule) : "Paused"} → {i.policy.repository}</span>}
                <span class="text-sm">{isTime(i.state.lastSuccess) ? `Last ${ago(i.state.lastSuccess).toLowerCase()}` : "Not yet"}</span>
                {i.activity && <Tag kind="update">{i.activity}</Tag>}
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

      <section aria-label="In the repositories" class="mt-8">
        <div class="mb-2 flex flex-wrap items-center gap-3">
          <div class="mr-auto max-w-[640px]">
            <h2 class="text-lg font-semibold">In the repositories</h2>
            <p class="text-sm text-muted">Everything the repositories hold, including removed environments and volumes and other machines' backups, to restore as new.</p>
          </div>
          <Button disabled={looking || !o?.repositories.length} onClick={() => void look()}>
            {looking ? "Looking…" : catalog ? "Look again" : "Look"}
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
  const items = o.catalog ?? [];
  return (
    <>
      {Object.entries(o.catalogErrors ?? {}).map(([repo, err]) => (
        <ErrorText key={repo} error={`${repo}: ${err}`} />
      ))}
      {!items.length ? (
        <p class="text-sm text-muted">The repositories are empty.</p>
      ) : (
        <ul class="m-0 list-none divide-y divide-hairline rounded-[10px] border border-line bg-surface p-0">
          {items.map((c) => (
            <li key={`${c.repository} ${c.host} ${c.item}`} class="flex flex-wrap items-center gap-x-4 gap-y-1 px-4 py-3">
              <div class="mr-auto min-w-0">
                <div class="font-semibold">{c.item.replace(":", " ")}</div>
                <div class="text-sm text-muted">
                  {c.snapshots} snapshot{c.snapshots === 1 ? "" : "s"}, latest {day(c.latest)} · {c.repository} · {c.host === o.host ? "this Deskyard" : c.host}
                </div>
              </div>
              {c.local ? (
                <a href={itemHref(c.item)} class="text-sm">
                  Open
                </a>
              ) : (
                <Button onClick={() => setRestoring(c)}>Restore latest as new</Button>
              )}
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

function RepositoryCard({ repository: r, used, onRemoved }: { repository: Repository; used: number; onRemoved: () => void }) {
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
      <div class="flex flex-wrap items-start gap-x-4 gap-y-2">
        <div class="mr-auto min-w-0">
          <h3 class="text-lg font-semibold">{r.name}</h3>
          <p class="mono text-sm break-all text-muted">
            {r.remote}:{r.folder}
          </p>
        </div>
        <Button onClick={() => (key ? setKey(null) : void reveal())}>{key ? "Hide recovery key" : "Show recovery key"}</Button>
        <Button onClick={() => setRemoving(true)}>Remove</Button>
      </div>
      <p class="mt-3 text-sm text-muted">
        Added {day(r.created)}. {used ? `${used} item${used === 1 ? "" : "s"} back up here.` : "Nothing backs up here yet."}
      </p>
      {key && <KeyPanel repository={r} recoveryKey={key} />}
      <ErrorText error={error} />
      {removing && <RemoveRepository repository={r} onClose={() => setRemoving(false)} onRemoved={onRemoved} />}
    </article>
  );
}

function KeyPanel({ repository: r, recoveryKey }: { repository: Repository; recoveryKey: string }) {
  return (
    <div class="mt-3 rounded-lg border border-line bg-white p-3.5">
      <div class="flex flex-wrap items-center gap-2">
        <code class="mono min-w-0 flex-1 break-all text-sm" aria-label="Recovery key">
          {recoveryKey}
        </code>
        <Button
          onClick={() =>
            void navigator.clipboard.writeText(recoveryKey).then(
              () => notify("Copied the recovery key"),
              () => notify("Couldn't copy; select the key and copy it instead", { error: true }),
            )
          }
        >
          Copy
        </Button>
      </div>
      <p class="mt-2 text-[13px] text-muted">
        Keep it in a password manager: without it the backups can't be read, even with the remote's credentials. By hand:{" "}
        <span class="mono">
          restic -r rclone:{r.remote}:…/{r.folder} snapshots
        </span>{" "}
        with the remote's rclone configuration.
      </p>
    </div>
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
  const valid = NAME.test(name) && !!remote && !!folder.trim() && (!needsKey || !!key.trim());
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
        setError("There's already a backup repository in that folder. Enter its recovery key to connect it.");
      } else setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };
  if (created?.recoveryKey) {
    return (
      <Dialog title={`Created ${created.repository.name}`} onClose={onClose} onSubmit={onClose} footer={<Button type="submit" variant="primary">I've saved it</Button>}>
        <p class="text-sm">This is the repository's recovery key. Save it somewhere safe now; you can show it again later on this page.</p>
        <KeyPanel repository={created.repository} recoveryKey={created.recoveryKey} />
      </Dialog>
    );
  }
  return (
    <Dialog
      title="Add backup repository"
      intro="A new folder gets a new repository and recovery key. A folder that already holds one, from a reinstall or another Deskyard, is connected with its key."
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
      <Field label="Name" htmlFor="repo-name" hint={name && !NAME.test(name) ? <span class="text-danger">Use 2–32 lowercase letters, digits and dashes.</span> : undefined}>
        <input id="repo-name" value={name} autocomplete="off" spellcheck={false} onInput={(e) => setName(e.currentTarget.value.toLowerCase())} />
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
      <Field label="Folder on the remote" htmlFor="repo-folder" hint="Below the remote's root. It can't be changed later.">
        <input id="repo-folder" class="mono" value={folder} spellcheck={false} onInput={(e) => { setFolder(e.currentTarget.value); setNeedsKey(false); }} />
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
      <Note>
        Deskyard forgets the repository and its recovery key. Its snapshots stay on {r.remote}; to reach them again, add the repository with the same folder and its recovery key, so save the key first.
      </Note>
      <ErrorText error={error} />
    </Dialog>
  );
}
