import { useEffect, useState } from "preact/hooks";
import { api, type Desktop, type FileListing } from "../api.ts";
import { ago, bytes } from "../lib/format.ts";
import { btnClass, Button, Dialog, ErrorText, errorMessage } from "../components/ui.tsx";

const ROOT = "/workspace";

/** Browse a desktop's files under /workspace and download a file, or a folder as a zip. */
export function DownloadDialog({ desktop, onClose }: { desktop: Desktop; onClose: () => void }) {
  const [listing, setListing] = useState<FileListing | null>(null);
  const [box, setBox] = useState(ROOT);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  const go = async (path: string) => {
    setLoading(true);
    setError(null);
    try {
      const l = await api.files(desktop.name, path);
      setListing(l);
      setBox(l.path);
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void go(ROOT);
  }, [desktop.name]);

  const here = listing?.path ?? ROOT;
  const parent = here.slice(0, Math.max(here.lastIndexOf("/"), 0)) || ROOT;
  const join = (name: string) => `${here}/${name}`;
  // Folders first, each group newest first (the server's order).
  const entries = listing ? [...listing.entries.filter((e) => e.dir), ...listing.entries.filter((e) => !e.dir)] : [];

  return (
    <Dialog
      title={`Download from ${desktop.name}`}
      intro="Files under /workspace. A folder downloads as a zip."
      wide
      onClose={onClose}
      onSubmit={() => void go(box)}
      footer={
        <>
          <Button variant="quiet" onClick={onClose}>
            Close
          </Button>
          <a class={btnClass("primary")} href={api.downloadUrl(desktop.name, here)} download>
            Download this folder (.zip)
          </a>
        </>
      }
    >
      <div class="mb-3 flex gap-2">
        <Button aria-label="Up one folder" disabled={here === ROOT || loading} onClick={() => void go(parent)}>
          ↑
        </Button>
        <input
          type="text"
          class="mono min-w-0 flex-1"
          aria-label="Folder path"
          autocomplete="off"
          spellcheck={false}
          value={box}
          onInput={(e) => setBox(e.currentTarget.value)}
        />
        <Button type="submit" disabled={loading}>
          Go
        </Button>
      </div>
      <ErrorText error={error} />
      <ul class="m-0 mb-3 max-h-[340px] list-none overflow-auto p-0" aria-busy={loading}>
        {listing && entries.length === 0 && <li class="py-2 text-muted">This folder is empty.</li>}
        {entries.map((e) => (
          <li key={e.name} class="flex items-center gap-2.5 border-b border-line py-1.5 last:border-b-0">
            {e.dir ? (
              <button type="button" class="min-w-0 flex-1 truncate text-left font-semibold hover:underline" onClick={() => void go(join(e.name))}>
                <span aria-hidden="true">▸ </span>
                {e.name}
              </button>
            ) : (
              <span class="min-w-0 flex-1 truncate">{e.name}</span>
            )}
            <span class="flex-none text-[13px] text-muted">
              {e.dir ? "" : `${bytes(e.size)}, `}
              {ago(e.modified)}
            </span>
            <a
              class={btnClass("quiet", "h-[30px]! px-2.5!")}
              href={api.downloadUrl(desktop.name, join(e.name))}
              download
              aria-label={e.dir ? `Download ${e.name} as zip` : `Download ${e.name}`}
            >
              {e.dir ? "Zip" : "Download"}
            </a>
          </li>
        ))}
      </ul>
      {listing?.truncated && <p class="mb-3 text-[13px] text-muted">Showing the 2,000 newest entries. Type a path to reach the rest.</p>}
    </Dialog>
  );
}
