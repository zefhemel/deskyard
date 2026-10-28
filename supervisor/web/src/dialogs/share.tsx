// Share an environment (a token account with one grant and a login link), and
// the dialog that shows a login link once.
import { useState } from "preact/hooks";
import { api, type Level } from "../api.ts";
import { useCopied } from "../components/secret-reveal.tsx";
import { Button, Dialog, ErrorText, errorMessage, Field, Seg } from "../components/ui.tsx";
import { expiresAt, isoDate, levelLabel, type TimeLimit, timeLimits } from "../lib/access.ts";
import { USERNAME, usernameHint } from "../lib/names.ts";
import { day, until } from "../lib/format.ts";

const levels: { value: Level; label: string }[] = [
  { value: "full", label: levelLabel("full") },
  { value: "observe", label: levelLabel("observe") },
];

/** A time limit (none, an hour, a day, a week or a date) and what it means; `when` is its end. */
export function TimeLimitField({
  label,
  limit,
  date,
  onLimit,
  onDate,
  none,
  ends,
}: {
  label: string;
  limit: TimeLimit;
  date: string;
  onLimit: (l: TimeLimit) => void;
  onDate: (d: string) => void;
  /** The hint without a limit. */
  none: string;
  /** What happens when it ends. */
  ends: string;
}) {
  const when = expiresAt(limit, date);
  const tomorrow = isoDate(new Date(Date.now() + 86400 * 1000));
  return (
    <Field
      label={label}
      hint={
        when === null
          ? none
          : when === undefined
            ? "Pick the last day."
            : `${limit === "hour" ? "In an hour" : `On ${day(when.toISOString())}`}. ${ends}`
      }
    >
      <Seg label={label} options={timeLimits} value={limit} onChange={onLimit} />
      {limit === "date" && (
        <input
          type="date"
          class="mt-2.5 max-w-[220px]"
          min={tomorrow}
          value={date}
          onInput={(e) => onDate(e.currentTarget.value)}
          aria-label={`${label}: last day`}
        />
      )}
    </Field>
  );
}

export function ShareDialog({ environment, onClose, onShared }: { environment: string; onClose: () => void; onShared: (username: string, link: string) => void }) {
  const [username, setUsername] = useState("");
  const [level, setLevel] = useState<Level>("full");
  const [limit, setLimit] = useState<TimeLimit>("none");
  const [date, setDate] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const when = expiresAt(limit, date);
  const badName = username !== "" && !USERNAME.test(username);
  const ready = USERNAME.test(username) && when !== undefined;

  const submit = async () => {
    if (!ready) return;
    setBusy(true);
    setError(null);
    try {
      const r = await api.createShare(environment, { username, level, expires: when ? when.toISOString() : null });
      onShared(r.share.username, r.link);
    } catch (e) {
      setError(errorMessage(e));
      setBusy(false);
    }
  };

  return (
    <Dialog
      title={`Share ${environment}`}
      intro="Makes a link that signs someone in to this environment, once. Send it the way you like."
      onClose={onClose}
      onSubmit={() => void submit()}
      footer={
        <>
          <Button variant="quiet" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" variant="primary" disabled={busy || !ready}>
            Share
          </Button>
        </>
      }
    >
      <Field
        label="Name"
        htmlFor="share-name"
        hint={badName ? <span class="text-danger">{usernameHint}</span> : "Who it's for. They appear under this name to everyone on the desktop."}
      >
        <input
          id="share-name"
          type="text"
          autocomplete="off"
          autocapitalize="none"
          spellcheck={false}
          placeholder="sanne"
          value={username}
          onInput={(e) => setUsername(e.currentTarget.value.toLowerCase())}
        />
      </Field>
      <Field
        label="Access"
        hint={
          level === "full"
            ? "Everything you can do on it: its desktop, terminals and files, starting and stopping it, and the CLI."
            : "Watching its desktop, nothing else: no keyboard, mouse or clipboard."
        }
      >
        <Seg label="Access" options={levels} value={level} onChange={setLevel} />
      </Field>
      <TimeLimitField
        label="Time limit"
        limit={limit}
        date={date}
        onLimit={setLimit}
        onDate={setDate}
        none="It works until you revoke it."
        ends="Then the share ends and its connections close."
      />
      <ErrorText error={error} />
    </Dialog>
  );
}

/** A login link, shown once, with Copy (in the footer, saying when it worked). */
export function LinkDialog({ username, link, onClose, title }: { username: string; link: string; onClose: () => void; title?: string }) {
  const { copied, copy } = useCopied(link);
  return (
    <Dialog
      title={title ?? `A link for ${username}`}
      intro={<>Copy it now: it won't be shown again.</>}
      onClose={onClose}
      onSubmit={onClose}
      wide
      footer={
        <>
          <Button onClick={() => void copy()}>{copied ? "Copied" : "Copy link"}</Button>
          <Button type="submit" variant="primary">
            Done
          </Button>
        </>
      }
    >
      <pre data-login-link class="mb-[18px] rounded-lg bg-bezel px-3.5 py-3 font-mono text-[13px] leading-normal break-all whitespace-pre-wrap text-[#e6ebf1] select-all">
        {link}
      </pre>
      <p class="mb-[18px] text-sm">
        Works once. Anyone with this link can sign in as <b>{username}</b> until it is used.
      </p>
    </Dialog>
  );
}

/** "Never", or the day and how long until then. */
export function Expiry({ expires }: { expires: string | null }) {
  if (!expires) return <>Never</>;
  return (
    <>
      {day(expires)} <span class="text-muted">({until(expires)})</span>
    </>
  );
}
