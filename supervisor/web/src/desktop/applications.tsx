import { useCallback, useEffect, useState } from "preact/hooks";
import { api, type ApplicationInventory, type AppWindow } from "../api.ts";
import { Button, Dialog } from "../components/ui.tsx";

export function Applications({ name, selected, onSelect, onWindows }: {
  name: string;
  selected: string | null;
  onSelect: (id: string) => void;
  onWindows: (windows: AppWindow[]) => void;
}) {
  const [open, setOpen] = useState(false);
  const close = useCallback(() => setOpen(false), []);
  const [query, setQuery] = useState("");
  const [inventory, setInventory] = useState<ApplicationInventory>({ apps: [], windows: [] });
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [loading, setLoading] = useState(false);
  useEffect(() => {
    if (!open && !selected) return;
    let alive = true;
    let timer: ReturnType<typeof setTimeout>;
    const load = async () => {
      try {
        const next = await api.applications(name, !open);
        if (!alive) return;
        if (!Array.isArray(next.windows) || !Array.isArray(next.apps)) throw new Error("Invalid application inventory");
        setInventory((old) => ({ ...next, apps: open ? next.apps : old.apps }));
        onWindows(next.windows);
        setError("");
      } catch (e) {
        if (alive) setError(e instanceof Error ? e.message : String(e));
      } finally {
        if (alive) { setLoading(false); timer = setTimeout(load, 1200); }
      }
    };
    setLoading(true);
    void load();
    return () => { alive = false; clearTimeout(timer); };
  }, [name, open, selected, onWindows]);

  const action = async (kind: "launch" | "activate", id: string) => {
    setBusy(true);
    setError("");
    try {
      const result = await api.applicationAction(name, kind, id);
      const next = await api.applications(name);
      setInventory(next);
      onWindows(next.windows);
      if (result.id) {
        // Also raise newly launched windows, including apps that start behind others.
        if (kind === "launch") await api.applicationAction(name, "activate", result.id);
        onSelect(result.id);
        setOpen(false);
      } else {
        setError("Application started. Choose its window below when it appears.");
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally { setBusy(false); }
  };
  const search = query.toLocaleLowerCase();
  const windows = inventory.windows.filter((w) => !w.parent && `${w.title} ${w.app}`.toLocaleLowerCase().includes(search));
  const apps = inventory.apps.filter((a) => a.name.toLocaleLowerCase().includes(search));
  return <>
    <button type="button" class="min-h-10 cursor-pointer rounded-md px-2.5 font-semibold hover:bg-bar-hover" aria-haspopup="dialog" aria-expanded={open} onClick={() => setOpen(true)}>Apps</button>
    {error && !open && <button type="button" class="text-amber-300" title={error} onClick={() => setOpen(true)}>!</button>}
    {open && <div class="text-ink"><Dialog title="Applications" onClose={close}
      intro="Launch or switch a window. Focus is shared with other viewers and CLI clients."
      footer={<Button onClick={() => setOpen(false)} disabled={busy}>Close</Button>}>
      <input class="mb-4 w-full rounded-lg border border-line bg-white p-3" placeholder="Search applications and windows" aria-label="Search applications and windows" value={query} onInput={(e) => setQuery(e.currentTarget.value)} />
      {error && <p role="status" class="mb-3 text-danger">{error}</p>}
      {busy && <p role="status">Opening application…</p>}
      {loading && !inventory.apps.length && <p role="status">Loading applications…</p>}
      <div class="max-h-[55dvh] overflow-y-auto">
        <h3 class="mb-2 font-semibold">Running windows</h3>
        {!windows.length && <p class="mb-4 text-muted">No matching windows.</p>}
        {windows.map((w) => <button type="button" key={w.id} disabled={busy} aria-label={`Switch to ${w.title}`} onClick={() => void action("activate", w.id)}
          class="mb-2 flex min-h-14 w-full cursor-pointer flex-col rounded-lg border border-line bg-white p-3 text-left hover:border-accent disabled:opacity-50">
          <span class="font-semibold">{w.title || w.app}</span><span class="text-xs text-muted">{w.app}{w.minimized ? " · minimized" : ""}</span>
        </button>)}
        <h3 class="mt-4 mb-2 font-semibold">Installed applications</h3>
        {apps.map((a) => <button type="button" key={a.id} disabled={busy} aria-label={`Launch ${a.name}`} onClick={() => void action("launch", a.id)}
          class="mb-2 min-h-12 w-full cursor-pointer rounded-lg border border-line bg-white p-3 text-left font-semibold hover:border-accent disabled:opacity-50">{a.name}</button>)}
        {!apps.length && !loading && <p class="text-muted">No matching applications.</p>}
      </div>
    </Dialog></div>}
  </>;
}
