import { useState } from "preact/hooks";
import { api, type Conflict } from "../api.ts";
import { useFetch } from "../lib/use-fetch.ts";
import { backupStatus } from "../lib/backup-policy.ts";
import { Page, PageHead } from "../components/topbar.tsx";
import { Tile } from "../components/tile.tsx";
import { Button } from "../components/ui.tsx";
import { NewEnvironmentDialog } from "../dialogs/new-environment.tsx";
import { useLive, useNow } from "../live.tsx";

export function EnvironmentsPage() {
  const { me, environments, conflicts, jobs } = useLive();
  const now = useNow(10000);
  const [creating, setCreating] = useState(false);
  // Backups whose last run failed, shown on their tiles (admins only).
  const backups = useFetch(() => (me.admin ? api.backups() : Promise.resolve(null)), "backups");
  const failing = new Set((backups.data?.items ?? []).filter((i) => backupStatus(i)?.kind === "danger").map((i) => i.item));

  return (
    <Page>
      <PageHead title="Environments" sub={me.admin ? undefined : "The environments you can use. Ask an admin for more."}>
        {me.admin && (
          <Button variant="primary" onClick={() => setCreating(true)}>
            New environment
          </Button>
        )}
      </PageHead>
      {conflicts.length > 0 && <Conflicts conflicts={conflicts} />}
      {environments === null ? (
        <p class="text-muted">Loading…</p>
      ) : environments.length === 0 ? (
        <Empty admin={me.admin} onCreate={() => setCreating(true)} />
      ) : (
        <div class="grid grid-cols-[repeat(auto-fill,minmax(300px,1fr))] gap-x-6 gap-y-7 max-sm:grid-cols-1">
          {environments.map((c) => (
            <Tile key={c.name} environment={c} job={jobs[c.name]} now={now} admin={me.admin} backupFailed={failing.has(`environment:${c.name}`)} />
          ))}
        </div>
      )}
      {creating && <NewEnvironmentDialog onClose={() => setCreating(false)} />}
    </Page>
  );
}

/**
 * Containers of this supervisor it can't match to a definition. It neither
 * adopts nor removes them; an admin removes the extra containers by hand.
 */
function Conflicts({ conflicts }: { conflicts: Conflict[] }) {
  return (
    <section role="alert" aria-label="Container conflicts" class="mb-6 rounded-[10px] border border-[#eec3be] bg-[#fbeceb] px-5 py-4 text-sm">
      <h2 class="mb-1.5 text-[15px] font-semibold text-danger">
        {conflicts.length === 1 ? "A container conflict" : `${conflicts.length} container conflicts`}
      </h2>
      <p class="mb-2 text-muted">
        Deskyard leaves these containers alone until they are removed with the container engine. A duplicate blocks its environment's start, stop
        and settings.
      </p>
      <ul class="m-0 flex list-none flex-col gap-1 p-0">
        {conflicts.map((c) => (
          <li key={`${c.kind}:${c.name}`}>
            <span class="mono">{c.containers.join(", ")}</span>: {c.message}
          </li>
        ))}
      </ul>
    </section>
  );
}

function Empty({ admin, onCreate }: { admin: boolean; onCreate: () => void }) {
  return (
    <div class="rounded-[10px] border border-dashed border-line px-6 py-12 text-center text-muted">
      {admin ? (
        <>
          <p class="mb-4">No environments yet.</p>
          <Button variant="primary" onClick={onCreate}>
            New environment
          </Button>
        </>
      ) : (
        <p>You can't use any environments yet. Ask an admin for access.</p>
      )}
    </div>
  );
}
