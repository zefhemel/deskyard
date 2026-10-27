// The signed-in account's page (/account): its signed-in devices (the CLI
// clients holding one of its credentials) and signing out everywhere. Admins
// see another account's devices on the Accounts page through DeviceList.
import { useState } from "preact/hooks";
import { api, type Credential } from "../api.ts";
import { Page, PageHead } from "../components/topbar.tsx";
import { notify, notifyError } from "../components/toast.tsx";
import { Button, ErrorText, Tag } from "../components/ui.tsx";
import { SignOutEverywhereDialog } from "../dialogs/sign-out-everywhere.tsx";
import { useLive } from "../live.tsx";
import { ago, day } from "../lib/format.ts";
import { useFetch } from "../lib/use-fetch.ts";

const th = "border-b border-line px-4 py-[11px] text-left text-[13px] font-semibold text-muted";
const td = "border-b border-hairline px-4 py-[13px] align-middle";

export function DevicesPage() {
  const { me } = useLive();
  const devices = useFetch(() => api.credentials(), "me");
  const [everywhere, setEverywhere] = useState(false);
  return (
    <Page>
      <PageHead title={me.username} sub={me.share ? "A share" : me.kind === "token" ? "Signs in with login links" : undefined} />
      <section class="mb-8">
        <h2 class="mb-1 text-lg font-semibold">Signed-in devices</h2>
        <p class="mb-3.5 text-sm text-muted">
          The <span class="mono">deskyard</span> command on each machine where you ran <span class="mono">deskyard login</span> or{" "}
          <span class="mono">deskyard auth</span>. Signing one out ends its open shells and connections at once.
        </p>
        <DeviceList devices={devices.data} error={devices.error} revoke={(id) => api.revokeCredential(id)} onChanged={devices.reload} />
      </section>
      <section>
        <h2 class="mb-1 text-lg font-semibold">Browsers</h2>
        <p class="mb-3.5 text-sm text-muted">
          Signing out everywhere ends your sessions in every other browser, and closes their desktops and terminals. This browser stays signed in;
          signed-in devices aren't affected.
        </p>
        <Button onClick={() => setEverywhere(true)}>Sign out everywhere</Button>
      </section>
      {everywhere && <SignOutEverywhereDialog onClose={() => setEverywhere(false)} />}
    </Page>
  );
}

/** A list of credentials with Sign out on each; `devices` undefined while loading. */
export function DeviceList({
  devices,
  error,
  revoke,
  onChanged,
}: {
  devices: Credential[] | undefined;
  error: string | null;
  revoke: (id: string) => Promise<void>;
  onChanged: () => void;
}) {
  if (error) return <ErrorText error={error} />;
  if (!devices) return <p class="text-sm text-muted">Loading…</p>;
  if (!devices.length) return <p class="text-sm text-muted">No signed-in devices.</p>;
  return (
    <div class="overflow-x-auto rounded-[10px] border border-line bg-surface">
      <table class="w-full border-collapse text-sm">
        <thead>
          <tr>
            <th class={th}>Device</th>
            <th class={th}>Signed in</th>
            <th class={th}>Last used</th>
            <th class={th} />
          </tr>
        </thead>
        <tbody>
          {devices.map((d) => (
            <DeviceRow key={d.id} device={d} revoke={revoke} onChanged={onChanged} />
          ))}
        </tbody>
      </table>
    </div>
  );
}

function DeviceRow({ device: d, revoke, onChanged }: { device: Credential; revoke: (id: string) => Promise<void>; onChanged: () => void }) {
  const [busy, setBusy] = useState(false);
  const signOut = async () => {
    setBusy(true);
    try {
      await revoke(d.id);
      notify(`Signed out ${d.name}`);
      onChanged();
    } catch (e) {
      notifyError(e);
      setBusy(false);
    }
  };
  return (
    <tr class="last:[&>td]:border-b-0">
      <td class={td}>
        <span class="font-semibold">{d.name}</span> {d.current && <Tag kind="ok">this one</Tag>}
      </td>
      <td class={`${td} whitespace-nowrap`}>{day(d.created)}</td>
      <td class={`${td} whitespace-nowrap`}>{ago(d.lastUsed)}</td>
      <td class={`${td} text-right whitespace-nowrap`}>
        <Button class="h-[30px]! px-[11px]!" disabled={busy} onClick={() => void signOut()}>
          Sign out
        </Button>
      </td>
    </tr>
  );
}
