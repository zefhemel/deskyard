import { useState } from "preact/hooks";
import { Page, PageHead } from "../components/topbar.tsx";
import { Tile } from "../components/tile.tsx";
import { Button } from "../components/ui.tsx";
import { NewDesktopDialog } from "../dialogs/new-desktop.tsx";
import { useLive, useNow } from "../live.tsx";

export function DesktopsPage() {
  const { me, desktops, jobs } = useLive();
  const now = useNow(10000);
  const [creating, setCreating] = useState(false);

  return (
    <Page>
      <PageHead title="Desktops" sub={me.admin ? undefined : "The desktops you can use. Ask an admin for more."}>
        {me.admin && (
          <Button variant="primary" onClick={() => setCreating(true)}>
            New desktop
          </Button>
        )}
      </PageHead>
      {desktops === null ? (
        <p class="text-muted">Loading…</p>
      ) : desktops.length === 0 ? (
        <Empty admin={me.admin} onCreate={() => setCreating(true)} />
      ) : (
        <div class="grid grid-cols-[repeat(auto-fill,minmax(300px,1fr))] gap-x-6 gap-y-7 max-sm:grid-cols-1">
          {desktops.map((c) => (
            <Tile key={c.name} desktop={c} job={jobs[c.name]} now={now} admin={me.admin} />
          ))}
        </div>
      )}
      {creating && <NewDesktopDialog onClose={() => setCreating(false)} />}
    </Page>
  );
}

function Empty({ admin, onCreate }: { admin: boolean; onCreate: () => void }) {
  return (
    <div class="rounded-[10px] border border-dashed border-line px-6 py-12 text-center text-muted">
      {admin ? (
        <>
          <p class="mb-4">No desktops yet.</p>
          <Button variant="primary" onClick={onCreate}>
            New desktop
          </Button>
        </>
      ) : (
        <p>You can't use any desktops yet. Ask an admin for access.</p>
      )}
    </div>
  );
}
