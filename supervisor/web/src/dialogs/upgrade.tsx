import type { ComponentChildren } from "preact";
import type { JobRef } from "../api.ts";
import { JobProgress, useJob } from "../components/job.tsx";
import { SecretImageNote } from "../components/secret-access.tsx";
import { Button, Dialog, ErrorText, Note } from "../components/ui.tsx";

/** What survives an upgrade, as the Images page puts it. */
export const UPGRADE_NOTE = (
  <>
    Everything in <span class="mono">/workspace</span> stays: files, tools from mise, uv and npm, Flatpak apps, browser profile.
    Packages installed with apt don't.
  </>
);

/**
 * Confirms an upgrade (one desktop or every desktop behind on an image), then
 * follows its job. With `secrets` (the image contains secrets and others can
 * use the desktops) it says so first, as giving access does.
 */
export function UpgradeDialog({
  title,
  intro,
  action,
  secrets,
  begin,
  onClose,
}: {
  title: string;
  intro: ComponentChildren;
  action: string;
  secrets?: { image: string; who: string[] };
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
          <Button type="submit" variant={secrets ? "danger" : "primary"} disabled={run.running}>
            {run.running ? "Upgrading…" : action}
          </Button>
        </>
      }
    >
      {secrets && <SecretImageNote image={secrets.image} who={secrets.who} />}
      <Note>{UPGRADE_NOTE}</Note>
      <JobProgress job={run.job} />
      <ErrorText error={run.error} />
    </Dialog>
  );
}
