// The signed-in account's page (/account): its signed-in devices (the CLI
// clients holding one of its credentials, and its WebDAV password), its
// WebDAV drives and signing out everywhere. Admins
// see another account's devices on the Accounts page through DeviceList.
import { useState } from "preact/hooks";
import { api, type Credential } from "../api.ts";
import { copySecret, SecretReveal } from "../components/secret-reveal.tsx";
import { Page, PageHead } from "../components/topbar.tsx";
import { notify, notifyError } from "../components/toast.tsx";
import { Button, ErrorText, Note, Tag } from "../components/ui.tsx";
import { SignOutEverywhereDialog } from "../dialogs/sign-out-everywhere.tsx";
import { useLive } from "../live.tsx";
import { ago, day } from "../lib/format.ts";
import { useFetch } from "../lib/use-fetch.ts";
import { davAddress, davInTheClear } from "../lib/webdav.ts";

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
          <span class="mono">deskyard auth</span>, and your WebDAV password. Signing one out ends its open shells and connections at once.
        </p>
        <DeviceList devices={devices.data} error={devices.error} revoke={(id) => api.revokeCredential(id)} onChanged={devices.reload} />
      </section>
      {me.kind === "password" && !me.share && <WebDAVSection devices={devices.data} onChanged={devices.reload} />}
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

/** The account's WebDAV drives and its WebDAV password. */
function WebDAVSection({ devices, onChanged }: { devices: Credential[] | undefined; onChanged: () => void }) {
  const { me, environments } = useLive();
  const [password, setPassword] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const drives = (environments ?? []).filter((c) => c.webdav && c.level === "full").map((c) => c.name);
  const has = devices?.some((d) => d.kind === "webdav") ?? false;
  const change = async (make: boolean) => {
    setBusy(true);
    try {
      if (make) {
        setPassword((await api.createWebDAVPassword()).password);
      } else {
        await api.removeWebDAVPassword();
        setPassword(null);
        notify("Removed the WebDAV password");
      }
      onChanged();
    } catch (e) {
      notifyError(e);
    } finally {
      setBusy(false);
    }
  };
  return (
    <section class="mb-8">
      <h2 class="mb-1 text-lg font-semibold">WebDAV</h2>
      <p class="mb-3.5 text-sm text-muted">
        Mount an environment as a drive: in Finder, Go → Connect to Server; on an iPhone or iPad, a WebDAV app such as Owlfiles. Sign in as{" "}
        <span class="mono">{me.username}</span> with your password, or with a WebDAV password if you make one. It shows the home folder and the local volumes,
        also while the environment is stopped.
      </p>
      {drives.length ? (
        <ul class="mb-3.5 space-y-1.5 text-sm">
          {drives.map((name) => (
            <li key={name} class="flex flex-wrap items-center gap-2">
              <span class="mono min-w-0 break-all">{davAddress(name)}</span>
              <Button class="h-[30px]! px-[11px]!" onClick={() => void copySecret(davAddress(name), "the address")}>
                Copy
              </Button>
            </li>
          ))}
        </ul>
      ) : (
        <p class="mb-3.5 text-sm text-muted">None of your environments has WebDAV switched on. An admin switches it on in an environment&apos;s Access tab.</p>
      )}
      {davInTheClear() && <Note danger>This page is on plain HTTP, so a password given to a WebDAV client crosses the network readable. Windows refuses to connect this way.</Note>}
      {password && (
        <SecretReveal
          class="mb-3.5 bg-surface"
          secret={password}
          label="WebDAV password"
          what="the WebDAV password"
          note="Shown only now. Keep it in your WebDAV client or a password manager."
        />
      )}
      <div class="flex flex-wrap gap-2">
        <Button disabled={busy} onClick={() => void change(true)}>
          {has ? "Replace the WebDAV password" : "Make a WebDAV password"}
        </Button>
        {has && (
          <Button disabled={busy} onClick={() => void change(false)}>
            Remove it
          </Button>
        )}
      </div>
      <p class="mt-2 text-[13px] text-muted">
        {has
          ? "WebDAV takes only the WebDAV password, not your account password. Replacing it disconnects the clients using the old one."
          : "A WebDAV password replaces your account password for WebDAV only, so your account password isn't stored in WebDAV clients."}
      </p>
    </section>
  );
}
