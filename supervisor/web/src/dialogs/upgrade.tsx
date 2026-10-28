import type { ComponentChildren } from "preact";
import type { JobRef } from "../api.ts";
import { JobProgress, useJob } from "../components/job.tsx";
import { Button, Dialog, ErrorText } from "../components/ui.tsx";

/** What survives an upgrade, as the Images page puts it. */
export const UPGRADE_NOTE = (
  <>
    Everything in <span class="mono">/workspace</span> stays: files, tools from mise, uv and npm, Flatpak apps, browser profile.
    Packages installed with apt don't.
  </>
);

/** Confirms an upgrade (one environment or every environment behind on an image), then follows its job. */
export function UpgradeDialog({
  title,
  intro,
  action,
  begin,
  onClose,
}: {
  title: string;
  intro: ComponentChildren;
  action: string;
  begin: () => Promise<JobRef>;
  onClose: () => void;
}) {
  const run = useJob(() => onClose());
  return (
    <Dialog
      title={title}
      intro={intro}
      onClose={onClose}
      onSubmit={() => void run.start(begin)}
      footer={
        <>
          <Button variant="quiet" onClick={onClose}>
            {run.running ? "Close" : "Cancel"}
          </Button>
          <Button type="submit" variant="primary" disabled={run.running}>
            {run.running ? "Upgrading…" : action}
          </Button>
        </>
      }
    >
      <JobProgress job={run.job} />
      <ErrorText error={run.error} />
    </Dialog>
  );
}
