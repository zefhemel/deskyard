import { useEffect, useRef, useState } from "preact/hooks";
import { uploadFile } from "../api.ts";
import { notify, notifyLive } from "../components/toast.tsx";
import { errorMessage } from "../components/ui.tsx";
import { bytes } from "../lib/format.ts";
import { useEventListener, useFrameWindowListener } from "../lib/use-event-listener.ts";

const hasFiles = (e: DragEvent) => !!e.dataTransfer && Array.from(e.dataTransfer.types).includes("Files");

/** Whether a dropped item is a folder (the browser can't give us its contents as one file). */
function isFolder(item: DataTransferItem | undefined, file: File): boolean {
  const entry = item?.webkitGetAsEntry?.();
  if (entry) return entry.isDirectory;
  return file.type === "" && file.size % 4096 === 0 && file.size <= 65536; // older browsers: a folder shows up as a tiny typeless "file"
}

const STALE_MS = 1000;

/**
 * Dropping files anywhere on the desktop page copies them into the environment's
 * ~/Desktop. A drag over the screen fires its events in the iframe's own
 * window, which never reach the page, so the same listeners go on both the
 * page and the frame (re-attached whenever the frame loads). In the frame
 * they listen first (capture, on its window) and keep a drag of files to
 * themselves: Selkies' client has its own drop-to-upload on its input layer,
 * which would take the drop (and stop it reaching us) and send it to an
 * upload the image keeps closed. While a drag is in progress an overlay
 * covers the screen and takes the drop. The overlay can't get stuck: it hides
 * when dragover events stop coming (the pointer left the window, or Escape
 * ended the drag), and on drop or dragend.
 */
export function DropZone({ environment, frame, onDone }: { environment: string; frame: HTMLIFrameElement | null; onDone?: () => void }) {
  const [dragging, setDragging] = useState(false);
  const lastSeen = useRef(0);

  const upload = (dt: DataTransfer | null) => {
    if (!dt) return;
    const items = Array.from(dt.items);
    Array.from(dt.files).forEach((f, i) => {
      if (isFolder(items[i], f)) notify(`${f.name} is a folder: drop files, not folders`, { error: true, ms: 6000 });
      else void send(environment, f).then(onDone);
    });
  };

  const seen = (e: DragEvent) => {
    if (!hasFiles(e)) return false;
    e.preventDefault(); // allow the drop, and keep the browser from opening the file
    if (e.dataTransfer) e.dataTransfer.dropEffect = "copy";
    lastSeen.current = Date.now();
    return true;
  };
  const over = (e: DragEvent) => {
    if (seen(e)) setDragging(true);
  };
  const drop = (e: DragEvent) => {
    if (!hasFiles(e)) return;
    e.preventDefault();
    setDragging(false);
    // A drop on the overlay is handled by the overlay; one that reached a window directly is handled here.
    if (!(e.target instanceof Element && e.target.closest("[data-testid=drop-overlay]"))) upload(e.dataTransfer);
  };
  const end = () => setDragging(false);
  // In the frame, before anything in it, and no further.
  const first = (f: (e: DragEvent) => void) => (e: DragEvent) => {
    if (hasFiles(e)) e.stopPropagation();
    f(e);
  };
  // Listeners on the page and on the frame's window.
  useEventListener(window, "dragenter", over);
  useEventListener(window, "dragover", over);
  useEventListener(window, "drop", drop);
  useEventListener(window, "dragend", end);
  useFrameWindowListener(frame, "dragenter", first(over), true);
  useFrameWindowListener(frame, "dragover", first(over), true);
  useFrameWindowListener(frame, "drop", first(drop), true);
  useFrameWindowListener(frame, "dragend", first(end), true);

  // Hide when the drag has gone quiet.
  useEffect(() => {
    if (!dragging) return;
    const t = window.setInterval(() => {
      if (Date.now() - lastSeen.current > STALE_MS) setDragging(false);
    }, 200);
    // Neither fires during a drag, so one means the drag is over.
    const hide = () => setDragging(false);
    window.addEventListener("pointerdown", hide);
    window.addEventListener("mousemove", hide);
    return () => {
      window.clearInterval(t);
      window.removeEventListener("pointerdown", hide);
      window.removeEventListener("mousemove", hide);
    };
  }, [dragging]);

  if (!dragging) return null;
  return (
    <div
      data-testid="drop-overlay"
      class="fixed inset-x-0 top-[46px] bottom-0 z-40 flex items-center justify-center border-2 border-dashed border-white/70 bg-[rgba(15,21,29,.72)] text-xl font-bold text-white"
      onDragOver={(e) => {
        e.preventDefault();
        lastSeen.current = Date.now();
        if (e.dataTransfer) e.dataTransfer.dropEffect = "copy";
      }}
      onDrop={(e) => {
        e.preventDefault();
        setDragging(false);
        upload(e.dataTransfer);
      }}
    >
      Drop to copy into {environment}
    </div>
  );
}

async function send(environment: string, file: File): Promise<void> {
  const toast = notifyLive(`Copying ${file.name}…`);
  try {
    const r = await uploadFile(environment, file, (f) => toast.set(`Copying ${file.name}… ${Math.round(f * 100)}%`));
    toast.finish(`Copied ${file.name} to ${r.path.split("/").pop()} (${bytes(r.bytes)})`);
  } catch (e) {
    toast.finish(`${file.name}: ${errorMessage(e)}`, { error: true });
  }
}
