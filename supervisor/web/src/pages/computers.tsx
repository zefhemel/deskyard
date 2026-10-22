import { useState } from "preact/hooks";
import { Page, PageHead } from "../components/topbar.tsx";
import { Tile } from "../components/tile.tsx";
import { Button } from "../components/ui.tsx";
import { NewComputerDialog } from "../dialogs/new-computer.tsx";
import { useLive, useNow } from "../live.tsx";

export function ComputersPage() {
  const { me, computers, jobs } = useLive();
  const now = useNow(10000);
  const [creating, setCreating] = useState(false);

  return (
    <Page>
      <PageHead title="Computers" sub={me.admin ? undefined : "The computers you can use. Ask an admin for more."}>
        {me.admin && (
          <Button variant="primary" onClick={() => setCreating(true)}>
            New computer
          </Button>
        )}
      </PageHead>
      {computers === null ? (
        <p class="text-muted">Loading…</p>
      ) : computers.length === 0 ? (
        <Empty admin={me.admin} onCreate={() => setCreating(true)} />
      ) : (
        <div class="grid grid-cols-[repeat(auto-fill,minmax(300px,1fr))] gap-x-6 gap-y-7 max-sm:grid-cols-1">
          {computers.map((c) => (
            <Tile key={c.name} computer={c} job={jobs[c.name]} now={now} admin={me.admin} />
          ))}
        </div>
      )}
      {creating && <NewComputerDialog onClose={() => setCreating(false)} />}
    </Page>
  );
}

function Empty({ admin, onCreate }: { admin: boolean; onCreate: () => void }) {
  return (
    <div class="rounded-[10px] border border-dashed border-line px-6 py-12 text-center text-muted">
      {admin ? (
        <>
          <p class="mb-4">No computers yet.</p>
          <Button variant="primary" onClick={onCreate}>
            New computer
          </Button>
        </>
      ) : (
        <p>You can't use any computers yet. Ask an admin for access.</p>
      )}
    </div>
  );
}
