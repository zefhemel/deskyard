// The Flatpak and remote rows of the recipe dialog.
import { useEffect, useState } from "preact/hooks";
import { searchFlathub, type FlathubApp } from "../lib/flathub.ts";
import { Button, IconButton } from "./ui.tsx";

export interface FlatpakRow {
  key: number;
  /** An app ID, or a .flatpakref URL. */
  value: string;
  /** Remote name; "" is Flathub. */
  remote: string;
}

export interface RemoteRow {
  key: number;
  name: string;
  mode: "repoFile" | "url";
  repoFile: string;
  url: string;
  gpgKey: string;
}

let keys = 1;
export const newKey = () => keys++;

export const isRefUrl = (v: string) => /^https?:\/\//i.test(v.trim());

const input = "min-w-0 flex-1";

/** Flathub search results to pick from; when the search is unreachable, says so and leaves the plain rows to type into. */
function FlathubSearch({ onPick }: { onPick: (app: FlathubApp) => void }) {
  const [q, setQ] = useState("");
  const [hits, setHits] = useState<FlathubApp[]>([]);
  const [offline, setOffline] = useState(false);

  useEffect(() => {
    const query = q.trim();
    if (query.length < 2) {
      setHits([]);
      return;
    }
    const ctl = new AbortController();
    const t = window.setTimeout(() => {
      searchFlathub(query, ctl.signal).then(
        (h) => {
          setOffline(false);
          setHits(h);
        },
        () => {
          if (!ctl.signal.aborted) {
            setOffline(true);
            setHits([]);
          }
        },
      );
    }, 300);
    return () => {
      ctl.abort();
      window.clearTimeout(t);
    };
  }, [q]);

  return (
    <div class="mb-2">
      <input
        type="text"
        aria-label="Search Flathub"
        placeholder="Search Flathub…"
        autocomplete="off"
        spellcheck={false}
        value={q}
        onInput={(e) => setQ(e.currentTarget.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") e.preventDefault();
        }}
      />
      {offline && <p class="mt-1.5 text-[13px] text-muted">Flathub's search isn't reachable from this browser. Type the app ID in a row below instead.</p>}
      {hits.length > 0 && (
        <div class="mt-1.5 max-h-[220px] overflow-auto rounded-lg border border-line bg-white">
          {hits.map((h) => (
            <button
              key={h.id}
              type="button"
              class="block w-full cursor-pointer border-b border-hairline px-3 py-2 text-left text-sm last:border-b-0 hover:bg-hairline"
              onClick={() => {
                onPick(h);
                setQ("");
              }}
            >
              <b class="font-semibold">{h.name}</b> <span class="mono text-[12.5px] text-muted">{h.id}</span>
              {h.summary && <div class="text-[13px] text-muted">{h.summary}</div>}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

export function FlatpakRows({ rows, remotes, onChange }: { rows: FlatpakRow[]; remotes: string[]; onChange: (rows: FlatpakRow[]) => void }) {
  const set = (key: number, patch: Partial<FlatpakRow>) => onChange(rows.map((r) => (r.key === key ? { ...r, ...patch } : r)));
  return (
    <div>
      <FlathubSearch onPick={(app) => !rows.some((r) => r.value === app.id) && onChange([...rows, { key: newKey(), value: app.id, remote: "" }])} />
      {rows.map((r) => (
        <div key={r.key} class="mb-2 flex items-center gap-2">
          <input
            type="text"
            class={`mono ${input}`}
            aria-label="Flatpak app ID or .flatpakref URL"
            placeholder="org.gimp.GIMP, or a .flatpakref URL"
            autocomplete="off"
            spellcheck={false}
            value={r.value}
            onInput={(e) => set(r.key, { value: e.currentTarget.value })}
          />
          {!isRefUrl(r.value) && (
            <select aria-label="Remote" class="w-auto max-w-[40%]" value={r.remote} onChange={(e) => set(r.key, { remote: e.currentTarget.value })}>
              <option value="">Flathub</option>
              {remotes.map((n) => (
                <option key={n} value={n}>
                  {n}
                </option>
              ))}
            </select>
          )}
          <IconButton aria-label="Remove this app" onClick={() => onChange(rows.filter((x) => x.key !== r.key))}>
            ×
          </IconButton>
        </div>
      ))}
      <Button onClick={() => onChange([...rows, { key: newKey(), value: "", remote: "" }])}>Add app by ID</Button>
    </div>
  );
}

export function RemoteRows({ rows, onChange }: { rows: RemoteRow[]; onChange: (rows: RemoteRow[]) => void }) {
  const set = (key: number, patch: Partial<RemoteRow>) => onChange(rows.map((r) => (r.key === key ? { ...r, ...patch } : r)));
  return (
    <div>
      {rows.map((r) => (
        <div key={r.key} class="mb-2.5 rounded-lg border border-line bg-white p-2.5">
          <div class="mb-2 flex items-center gap-2">
            <input
              type="text"
              class={input}
              aria-label="Remote name"
              placeholder="Name, like my-repo"
              autocomplete="off"
              spellcheck={false}
              value={r.name}
              onInput={(e) => set(r.key, { name: e.currentTarget.value })}
            />
            <select
              aria-label="How the remote is described"
              class="w-auto"
              value={r.mode}
              onChange={(e) => set(r.key, { mode: e.currentTarget.value as RemoteRow["mode"] })}
            >
              <option value="repoFile">.flatpakrepo file</option>
              <option value="url">URL and GPG key</option>
            </select>
            <IconButton aria-label="Remove this remote" onClick={() => onChange(rows.filter((x) => x.key !== r.key))}>
              ×
            </IconButton>
          </div>
          {r.mode === "repoFile" ? (
            <input
              type="text"
              class="mono"
              aria-label=".flatpakrepo URL"
              placeholder="https://example.org/repo.flatpakrepo"
              autocomplete="off"
              spellcheck={false}
              value={r.repoFile}
              onInput={(e) => set(r.key, { repoFile: e.currentTarget.value })}
            />
          ) : (
            <>
              <input
                type="text"
                class="mono mb-2"
                aria-label="Repository URL"
                placeholder="https://example.org/repo"
                autocomplete="off"
                spellcheck={false}
                value={r.url}
                onInput={(e) => set(r.key, { url: e.currentTarget.value })}
              />
              <input
                type="text"
                class="mono"
                aria-label="GPG key, base64"
                placeholder="GPG key, base64"
                autocomplete="off"
                spellcheck={false}
                value={r.gpgKey}
                onInput={(e) => set(r.key, { gpgKey: e.currentTarget.value })}
              />
            </>
          )}
        </div>
      ))}
      <Button onClick={() => onChange([...rows, { key: newKey(), name: "", mode: "repoFile", repoFile: "", url: "", gpgKey: "" }])}>Add remote</Button>
    </div>
  );
}
