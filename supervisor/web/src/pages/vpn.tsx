import { useCallback, useState } from "preact/hooks";
import { api, ApiError, type Job, type SavedVPNConnection, type VPNReference, type VPNInput } from "../api.ts";
import { JobProgress, useJob } from "../components/job.tsx";
import { Page, PageHead } from "../components/topbar.tsx";
import { Button, Check, Dialog, ErrorText, Field, Note, errorMessage } from "../components/ui.tsx";
import { VPNStatus } from "../components/vpn.tsx";
import { CandidateTestSession, connectionInput, connectionUpdate, type ConnectionForm } from "../lib/vpn-form.ts";
import { useFetch } from "../lib/use-fetch.ts";
import { useLive } from "../live.tsx";

export function VPNPage() {
  const list = useFetch(() => api.vpnConnections(), "vpn-connections");
  const [editing, setEditing] = useState<SavedVPNConnection | null | undefined>(undefined);
  const [removing, setRemoving] = useState<SavedVPNConnection | null>(null);
  return (
    <Page>
      <PageHead title="VPN connections" sub="Reusable NordVPN settings; each environment has its own connection.">
        <Button variant="primary" disabled={!list.data} onClick={() => setEditing(null)}>
          Add connection
        </Button>
      </PageHead>
      <ErrorText error={list.error} />
      {!list.data ? (
        <p class="text-muted">Loading…</p>
      ) : !list.data.connections.length ? (
        <p class="text-muted">No saved connections yet. Add NordVPN service credentials and a country to get started.</p>
      ) : (
        <div class="grid gap-3">
          {list.data.connections.map((c) => (
            <article key={c.id} class="flex flex-wrap items-center gap-4 rounded-[10px] border border-line bg-surface p-5">
              <div class="mr-auto">
                <h2 class="text-lg font-semibold">{c.name}</h2>
                <p class="text-sm text-muted">
                  NordVPN · {c.country} · Revision {c.revision}
                </p>
                <p class="mt-1 text-sm">
                  {c.computers.length ? (
                    <>
                      Used by{" "}
                      {c.computers.map((d, i) => (
                        <span key={d.id}>
                          {i > 0 ? ", " : ""}
                          <a href={`/environments/${d.name}`}>{d.name}</a>
                        </span>
                      ))}
                    </>
                  ) : (
                    "No environments use this connection."
                  )}
                </p>
              </div>
              <Button onClick={() => setEditing(c)}>Edit / test</Button>
              <Button onClick={() => setRemoving(c)}>Delete</Button>
            </article>
          ))}
        </div>
      )}
      <p class="mt-5 max-w-[760px] text-sm text-muted">
        Use NordVPN service credentials, which differ from your account login. A test uses a temporary connection and leaves environments unchanged. Stopped
        environments may keep a provider connection in use.
      </p>
      {editing !== undefined && list.data && (
        <ConnectionDialog
          key={editing?.id ?? "new"}
          initial={editing}
          countries={list.data.countries}
          onClose={() => setEditing(undefined)}
          onSaved={list.reload}
        />
      )}
      {removing && <DeleteConnection connection={removing} onClose={() => setRemoving(null)} onDeleted={list.reload} />}
    </Page>
  );
}

function ConnectionDialog({
  initial,
  countries,
  onClose,
  onSaved,
}: {
  initial: SavedVPNConnection | null;
  countries: string[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const [preview, setPreview] = useState(initial);
  // Secrets are write-only, including when reopening a saved connection.
  const [form, setForm] = useState<ConnectionForm>({ name: initial?.name ?? "", country: initial?.country ?? "", username: "", password: "" });
  const [confirmed, setConfirmed] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [jobID, setJobID] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [candidate] = useState(() => new CandidateTestSession());
  const [action, setAction] = useState<"test" | "save">("test");
  const { refresh } = useLive();
  const loadPreview = async () => {
    const list = await api.vpnConnections();
    const current = list.connections.find((c) => c.id === preview?.id);
    if (current) setPreview(current);
    setConfirmed(false);
    onSaved();
  };
  const run = useJob(() => {
    if (action === "save") {
      setForm((f) => ({ ...f, username: "", password: "" }));
      void loadPreview().catch((e) => setError(errorMessage(e)));
      refresh();
    }
  });
  const active = busy || run.running;
  const close = useCallback(() => {
    if (!active) onClose();
  }, [active, onClose]);
  const patch = (change: Partial<ConnectionForm>) => {
    setForm({ ...form, ...change });
    setConfirmed(false);
  };
  const start = async (kind: "test" | "save") => {
    if (active) return;
    setError(null);
    setJobID(null);
    const begin = (input: VPNInput) => {
      setAction(kind);
      return run.start(async () => {
        try {
          const ref =
            kind === "test"
              ? await api.testVPN({ ...input, ...(preview ? { connectionId: preview.id } : {}) })
              : await api.updateVPN(preview!.id, connectionUpdate(form, preview!));
          setJobID(ref.job);
          return ref;
        } catch (e) {
          if (e instanceof ApiError && e.status === 409) await loadPreview();
          throw e;
        }
      });
    };
    try {
      if (kind === "test") {
        await candidate.start(form, !!preview, begin);
      } else {
        const input = connectionInput(form, !!preview);
        if (preview?.computers.length && !confirmed) throw new Error("Confirm the affected environments' restart before saving.");
        if (!preview) {
          setBusy(true);
          const saved = await api.createVPN(input);
          setPreview({ ...saved, computers: [] });
          setForm({ ...form, username: "", password: "" });
          onSaved();
          onClose();
        } else await begin(input);
      }
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };
  const cancel = async () => {
    if (!jobID) return;
    try {
      await api.cancelJob(jobID);
    } catch (e) {
      setError(errorMessage(e));
    }
  };
  return (
    <Dialog
      title={preview ? `Edit ${preview.name}` : "Add VPN connection"}
      onClose={close}
      onSubmit={() => void start("save")}
      footer={
        <>
          <Button variant="quiet" disabled={active} onClick={onClose}>
            Close
          </Button>
          {run.running && (
            <Button onClick={() => void cancel()} disabled={!jobID}>
              Cancel job
            </Button>
          )}
          <Button disabled={active} onClick={() => void start("test")}>
            Test connection
          </Button>
          <Button type="submit" variant="primary" disabled={active || (!!preview?.computers.length && !confirmed)}>
            {active ? "Working…" : preview?.computers.length ? "Save and restart affected environments" : "Save connection"}
          </Button>
        </>
      }
    >
      <fieldset disabled={active} class="m-0 min-w-0 border-0 p-0">
        <Field label="Connection name" htmlFor="vpn-name">
          <input type="text" id="vpn-name" value={form.name} required onInput={(e) => patch({ name: e.currentTarget.value })} />
        </Field>
        <Field label="Provider">
          <p class="text-sm">NordVPN</p>
        </Field>
        <Field label="Country" htmlFor="vpn-country">
          <select id="vpn-country" value={form.country} required onChange={(e) => patch({ country: e.currentTarget.value })}>
            <option value="">Choose a country</option>
            {countries.map((c) => (
              <option key={c} value={c}>
                {c}
              </option>
            ))}
          </select>
        </Field>
        <p class="mb-4 text-sm text-muted">
          Use{" "}
          <a
            href="https://support.nordvpn.com/hc/en-us/articles/19685514639633-How-to-find-your-service-credentials-for-manual-NordVPN-setup"
            target="_blank"
            rel="noreferrer"
          >
            NordVPN service credentials
          </a>
          , separate from your account email and password.{preview && " Leave either field blank to keep its saved value."}
        </p>
        <Field label="Service username" htmlFor="vpn-username">
          <input type="text" id="vpn-username" autocomplete="off" value={form.username} onInput={(e) => patch({ username: e.currentTarget.value })} />
        </Field>
        <Field label="Service password" htmlFor="vpn-password">
          <input
            id="vpn-password"
            type="password"
            autocomplete="new-password"
            value={form.password}
            onInput={(e) => patch({ password: e.currentTarget.value })}
          />
        </Field>
        {preview?.computers.length ? (
          <Note>
            <p class="mb-2">
              Saving validates the candidate first, then restarts: <b>{preview.computers.map((d) => d.name).join(", ")}</b>. Open sessions briefly disconnect;
              files are kept. A failed validation keeps the saved connection.
            </p>
            <Check id="vpn-confirm" checked={confirmed} onChange={setConfirmed} title="Restart these affected environments" />
          </Note>
        ) : (
          <Note>Test connection checks these settings without changing an environment. A successful test describes availability at that time.</Note>
        )}
      </fieldset>
      <JobProgress job={run.job} />
      <ErrorText error={error || run.error} />
      <ConnectionResult job={action === "test" ? candidate.result(form, run.job) : run.job} action={action} />
      {preview && !run.job && (
        <p class="text-sm text-muted">
          Saved as {preview.name} · revision {preview.revision}.
        </p>
      )}
    </Dialog>
  );
}

function ConnectionResult({ job, action }: { job: Job | null; action: "test" | "save" }) {
  if (job?.status !== "done") return null;
  const result = job.result;
  if (result && "computers" in result)
    return (
      <div class="text-sm" role="status">
        <p class="mb-2">Saved revision {result.connection.revision}.</p>
        {result.computers.map((d) => (
          <p key={d.id} class="mb-2">
            {d.name}:{" "}
            {d.error ? (
              <>
                <span class="text-danger">{d.error}</span> <a href={`/environments/${d.name}`}>Retry start or restart</a>
              </>
            ) : (
              "Applied"
            )}
          </p>
        ))}
      </div>
    );
  return (
    <div role="status">
      <p class="mb-2 text-sm">{action === "test" ? "Connection test passed." : "Connection saved."}</p>
      {result && "state" in result && <VPNStatus vpn={result} />}
    </div>
  );
}

function DeleteConnection({ connection, onClose, onDeleted }: { connection: SavedVPNConnection; onClose: () => void; onDeleted: () => void }) {
  const [refs, setRefs] = useState<VPNReference[]>(connection.computers);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const remove = async () => {
    setBusy(true);
    setError(null);
    try {
      await api.deleteVPN(connection.id);
      onDeleted();
      onClose();
    } catch (e) {
      setError(errorMessage(e));
      if (e instanceof ApiError) setRefs(e.computers);
      onDeleted();
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog
      title={`Delete ${connection.name}?`}
      onClose={onClose}
      onSubmit={() => void remove()}
      footer={
        <>
          <Button variant="quiet" onClick={onClose}>
            Close
          </Button>
          <Button type="submit" variant="danger" disabled={busy || refs.length > 0}>
            Delete connection
          </Button>
        </>
      }
    >
      {refs.length ? (
        <p class="text-sm">
          Choose another connection or No VPN in these environments' settings first:{" "}
          {refs.map((d, i) => (
            <span key={d.id}>
              {i ? ", " : ""}
              <a href={`/environments/${d.name}?tab=settings`}>{d.name}</a>
            </span>
          ))}
          .
        </p>
      ) : (
        <p class="text-sm">This removes the saved connection and its credentials.</p>
      )}
      <ErrorText error={error} />
    </Dialog>
  );
}
