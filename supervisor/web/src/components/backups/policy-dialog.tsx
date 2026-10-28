// Setting up, changing or stopping an item's backups.
import { useState } from "preact/hooks";
import { api, type BackupItem, type ItemKind, type PolicyInput, type Repository } from "../../api.ts";
import { DAYS, policyDefaults, policyErrors } from "../../lib/backup-policy.ts";
import { Button, Check, Dialog, ErrorText, Field, Seg } from "../ui.tsx";
import { useAction } from "../use-action.ts";

export function PolicyDialog({
  kind,
  name,
  item,
  repositories,
  onClose,
  onSaved,
}: {
  kind: ItemKind;
  name: string;
  item: BackupItem;
  repositories: Repository[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const [p, setP] = useState<PolicyInput>(item.policy ?? policyDefaults(repositories));
  const errs = policyErrors(p);
  const valid = Object.keys(errs).length === 0;
  const done = () => {
    onSaved();
    onClose();
  };
  const save = useAction(() => api.putPolicy(kind, name, p), { success: `Saved ${name}'s backups`, onDone: done });
  const remove = useAction(() => api.deletePolicy(kind, name), { success: `${name} is no longer backed up; its snapshots are kept`, onDone: done });
  const busy = save.busy || remove.busy;
  const error = save.error || remove.error;
  const num = (k: keyof PolicyInput["retention"], label: string) => (
    <label class="flex flex-col gap-1 text-sm">
      {label}
      <input
        type="number"
        min={0}
        class="w-full"
        value={p.retention[k]}
        onInput={(e) => setP({ ...p, retention: { ...p.retention, [k]: Number(e.currentTarget.value) } })}
      />
    </label>
  );
  return (
    <Dialog
      title={`Back up ${name}`}
      wide
      onClose={onClose}
      onSubmit={() => valid && void save.run()}
      footer={
        <>
          {item.policy && (
            <Button variant="quiet" class="mr-auto" disabled={busy} onClick={() => void remove.run()}>
              Stop backing up
            </Button>
          )}
          <Button variant="quiet" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" variant="primary" disabled={!valid || busy}>
            Save
          </Button>
        </>
      }
    >
      <Field label="Repository" htmlFor="policy-repo">
        <select id="policy-repo" value={p.repository} onChange={(e) => setP({ ...p, repository: e.currentTarget.value })}>
          <option value="" disabled>
            Choose a repository
          </option>
          {repositories.map((r) => (
            <option key={r.name} value={r.name}>
              {r.name} ({r.remote}:{r.folder})
            </option>
          ))}
        </select>
      </Field>
      <Field label="When">
        <div class="flex flex-wrap items-center gap-2">
          <Seg
            label="How often"
            options={[
              { value: "hourly" as const, label: "Hourly" },
              { value: "daily" as const, label: "Daily" },
              { value: "weekly" as const, label: "Weekly" },
            ]}
            value={p.schedule.every}
            onChange={(every) => setP({ ...p, schedule: { ...p.schedule, every } })}
          />
          {p.schedule.every === "weekly" && (
            <select aria-label="Day" class="w-auto" value={p.schedule.weekday} onChange={(e) => setP({ ...p, schedule: { ...p.schedule, weekday: Number(e.currentTarget.value) } })}>
              {DAYS.map((d, i) => (
                <option key={d} value={i}>
                  {d}
                </option>
              ))}
            </select>
          )}
          <input
            type="time"
            aria-label="Time"
            class="w-auto"
            value={p.schedule.at}
            onInput={(e) => setP({ ...p, schedule: { ...p.schedule, at: e.currentTarget.value } })}
          />
        </div>
      </Field>
      <Field label="Keep" hint={errs.retention ? <span class="text-danger">{errs.retention}</span> : "Older snapshots are forgotten after each backup."}>
        <div class="grid grid-cols-3 gap-2 sm:grid-cols-6">
          {num("last", "Last")}
          {num("hourly", "Hourly")}
          {num("daily", "Daily")}
          {num("weekly", "Weekly")}
          {num("monthly", "Monthly")}
          {num("yearly", "Yearly")}
        </div>
      </Field>
      <Check id="policy-enabled" checked={p.enabled} onChange={(enabled) => setP({ ...p, enabled })} title="Back up on this schedule">
        Off pauses scheduled backups.
      </Check>
      <ErrorText error={error} />
    </Dialog>
  );
}
