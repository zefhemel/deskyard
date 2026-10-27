// WebDAV on the environment's Access tab: a way into its files, so it sits
// with who can use it. Admins switch it; others with full access and a
// password see where to connect while it's on.
import { useEffect, useState } from "preact/hooks";
import { api, type Environment } from "../../api.ts";
import { notify } from "../../components/toast.tsx";
import { Check, ErrorText, Note } from "../../components/ui.tsx";
import { useLive } from "../../live.tsx";
import { davAddress, davInTheClear, davView } from "../../lib/webdav.ts";

export function WebDAVAccess({ environment: c }: { environment: Environment }) {
  const { me } = useLive();
  const view = davView(c, me);
  if (!view) return null;
  return (
    <div class={me.admin ? "border-t border-line" : undefined}>
      <div class="max-w-[620px] px-5 py-5">
        <h2 class="mt-0 mb-1 text-base font-semibold">WebDAV</h2>
        {view === "switch" ? (
          <WebDAVSwitch environment={c} />
        ) : (
          <p class="mt-0 mb-0 text-sm text-muted">
            Mount {c.name}&apos;s files as a drive at <span class="mono break-all">{davAddress(c.name)}</span>, signing in with your username and
            password, or your WebDAV password from your account page.
          </p>
        )}
      </div>
    </div>
  );
}

/** Saves at once, independently of the accounts above. */
function WebDAVSwitch({ environment: c }: { environment: Environment }) {
  const [enabled, setEnabled] = useState(c.webdav);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => setEnabled(c.webdav), [c.name, c.webdav]);
  const save = async (value: boolean) => {
    setSaving(true);
    setEnabled(value);
    setError(null);
    try {
      await api.setWebDAV(c.name, value);
      notify(value ? "WebDAV switched on" : "WebDAV switched off");
    } catch (err) {
      setEnabled(!value);
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setSaving(false);
    }
  };
  return (
    <>
      <fieldset disabled={saving} class="m-0 min-w-0 border-0 p-0">
        <Check id="webdav" checked={enabled} onChange={(v) => void save(v)} title="Serve files over WebDAV">
          Lets everyone with full access and a password mount the home folder and local volumes as a drive in Finder, Windows or a WebDAV app, also
          while {c.name} is stopped. Host volumes aren&apos;t included. Saves immediately.
        </Check>
      </fieldset>
      {enabled && (
        <p class="-mt-2 mb-3 text-sm">
          Address: <span class="mono break-all">{davAddress(c.name)}</span>
        </p>
      )}
      {enabled && davInTheClear() && (
        <Note danger>
          This page is on plain HTTP, so WebDAV passwords would cross the network readable by anyone on the way. Windows refuses to connect this way.
        </Note>
      )}
      <ErrorText error={error} />
    </>
  );
}
