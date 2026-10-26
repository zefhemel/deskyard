import { useState } from "preact/hooks";
import { api, type CreatedToken } from "../api.ts";
import { Button, Dialog, ErrorText, errorMessage, Field, Note, Seg } from "../components/ui.tsx";
import { copyText } from "../lib/clipboard.ts";
import { day, duration } from "../lib/format.ts";

type Expiry = "never" | 7 | 30 | 90 | "date";

const expiryOptions: { value: Expiry; label: string }[] = [
  { value: "never", label: "Never" },
  { value: 7, label: "7 days" },
  { value: 30, label: "30 days" },
  { value: 90, label: "90 days" },
  { value: "date", label: "Date…" },
];

function isoDate(d: Date): string {
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
}

/** When the token expires, or null for never; undefined while the date is missing. */
function expiresAt(expiry: Expiry, date: string): Date | null | undefined {
  if (expiry === "never") return null;
  if (expiry === "date") return date ? new Date(`${date}T23:59:59`) : undefined;
  return new Date(Date.now() + expiry * 86400 * 1000);
}

export function NewTokenDialog({ environment, onClose, onCreated }: { environment: string; onClose: () => void; onCreated: (t: CreatedToken) => void }) {
  const [name, setName] = useState("");
  const [expiry, setExpiry] = useState<Expiry>(30);
  const [date, setDate] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const when = expiresAt(expiry, date);
  const tomorrow = isoDate(new Date(Date.now() + 86400 * 1000));

  const submit = async () => {
    if (!name.trim() || when === undefined) return;
    setBusy(true);
    setError(null);
    try {
      onCreated(await api.createToken(environment, { name: name.trim(), expires: when ? when.toISOString() : null }));
    } catch (e) {
      setError(errorMessage(e));
      setBusy(false);
    }
  };

  return (
    <Dialog
      title={`New access token for ${environment}`}
      intro={
        <>
          Use {environment} from your own machine: its shell, files, browser and desktop as <span class="mono">user</span>, and
          nothing else.
        </>
      }
      onClose={onClose}
      onSubmit={submit}
      footer={
        <>
          <Button variant="quiet" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" variant="primary" disabled={busy || !name.trim() || when === undefined}>
            Create token
          </Button>
        </>
      }
    >
      <Field label="Name" htmlFor="tn" hint="Who or what will use it, so you recognise it later.">
        <input id="tn" type="text" autocomplete="off" placeholder="My laptop" value={name} onInput={(e) => setName(e.currentTarget.value)} />
      </Field>
      <Field
        label="Expires"
        hint={
          when === null
            ? "It works until someone revokes it."
            : when === undefined
              ? "Pick the last day it works."
              : `On ${day(when.toISOString())}. Its open shells and browser connections close when it expires.`
        }
      >
        <Seg label="Expires" options={expiryOptions} value={expiry} onChange={setExpiry} />
        {expiry === "date" && (
          <input type="date" class="mt-2.5 max-w-[220px]" min={tomorrow} value={date} onInput={(e) => setDate(e.currentTarget.value)} aria-label="Expiry date" />
        )}
      </Field>
      <ErrorText error={error} />
    </Dialog>
  );
}

/** The `deskyard remote add` line, broken before --token as in a shell. */
function snippet(remoteAdd: string): string {
  return remoteAdd.replace(/ --token /, " \\\n  --token ");
}

export function TokenCreatedDialog({ environment, token: t, onClose }: { environment: string; token: CreatedToken; onClose: () => void }) {
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    setCopied(await copyText(t.remoteAdd));
  };
  const expires = t.expires
    ? `${day(t.expires)} (${duration(Math.round((Date.parse(t.expires) - Date.parse(t.created)) / 86400000) * 86400)})`
    : "Never";

  return (
    <Dialog
      title="Token created"
      intro="Copy it now: it won't be shown again. On your machine, run:"
      onClose={onClose}
      onSubmit={onClose}
      wide
      footer={
        <>
          <Button onClick={copy}>{copied ? "Copied" : "Copy command"}</Button>
          <Button type="submit" variant="primary">
            Done
          </Button>
        </>
      }
    >
      <pre class="mb-[18px] rounded-lg bg-bezel px-3.5 py-3 font-mono text-[13px] leading-normal break-all whitespace-pre-wrap text-[#e6ebf1] select-all">
        {snippet(t.remoteAdd)}
      </pre>
      <dl class="mb-[18px] grid grid-cols-[max-content_1fr] gap-x-[18px] gap-y-[9px] text-sm">
        <dt class="text-muted">Name</dt>
        <dd class="m-0">{t.name}</dd>
        <dt class="text-muted">Environment</dt>
        <dd class="m-0">{environment}</dd>
        <dt class="text-muted">Expires</dt>
        <dd class="m-0">{expires}</dd>
      </dl>
      <Note>
        Then run <span class="mono">deskyard -e {environment} guide</span> to learn the rest.
      </Note>
    </Dialog>
  );
}
