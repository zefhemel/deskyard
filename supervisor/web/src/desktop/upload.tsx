import type { RefObject } from "preact";
import { useEffect, useRef, useState } from "preact/hooks";
import { uploadFile } from "../api.ts";
import { notify, notifyLive } from "../components/toast.tsx";
import { errorMessage } from "../components/ui.tsx";
import { bytes } from "../lib/format.ts";

const hasFiles = (e: DragEvent) => !!e.dataTransfer && Array.from(e.dataTransfer.types).includes("Files");

/** Whether a dropped item is a folder (the browser can't give us its contents as one file). */
function isFolder(item: DataTransferItem | undefined, file: File): boolean {
  const entry = item?.webkitGetAsEntry?.();
  if (entry) return entry.isDirectory;
  return file.type === "" && file.size % 4096 === 0 && file.size <= 65536; // older browsers: a folder shows up as a tiny typeless "file"
}

const STALE_MS = 1000;

/**
 * Dropping files anywhere on the desktop page copies them into the desktop's
 * ~/Desktop. A drag over the screen fires its events in the iframe's own
 * window, which never reach the page, so the same listeners go on both the
 * page and the frame (re-attached whenever the frame loads). While a drag is
 * in progress an overlay covers the screen and takes the drop. The overlay
 * can't get stuck: it hides when dragover events stop coming (the pointer left
 * the window, or Escape ended the drag), and on drop or dragend.
 */
export function DropZone({ desktop, frame, frameKey, onDone }: { desktop: string; frame: RefObject<HTMLIFrameElement | null>; frameKey: number; onDone?: () => void }) {
  const [over, setOver] = useState(false);
  const lastSeen = useRef(0);
  const desktopRef = useRef(desktop);
  desktopRef.current = desktop;

  const upload = (dt: DataTransfer | null) => {
    if (!dt) return;
    const items = Array.from(dt.items);
    Array.from(dt.files).forEach((f, i) => {
      if (isFolder(items[i], f)) notify(`${f.name} is a folder: drop files, not folders`, { error: true, ms: 6000 });
      else void send(desktopRef.current, f).then(onDone);
    });
  };

  // Listeners on the page and on the frame's window.
  useEffect(() => {
    const seen = (e: DragEvent) => {
      if (!hasFiles(e)) return false;
      e.preventDefault(); // allow the drop, and keep the browser (or KasmVNC) from taking the file
      lastSeen.current = Date.now();
      return true;
    };
    const enter = (e: DragEvent) => {
      if (seen(e)) setOver(true);
    };
    const move = (e: DragEvent) => {
      if (seen(e)) setOver(true);
    };
    const drop = (e: DragEvent) => {
      if (!hasFiles(e)) return;
      e.preventDefault();
      setOver(false);
      // A drop on the overlay is handled by the overlay; one that reached a window directly is handled here.
      if (!(e.target instanceof Element && e.target.closest("[data-testid=drop-overlay]"))) upload(e.dataTransfer);
    };
    const end = () => setOver(false);
    const attach = (w: Window) => {
      w.addEventListener("dragenter", enter as EventListener);
      w.addEventListener("dragover", move as EventListener);
      w.addEventListener("drop", drop as EventListener);
      w.addEventListener("dragend", end);
      return () => {
        try {
          w.removeEventListener("dragenter", enter as EventListener);
          w.removeEventListener("dragover", move as EventListener);
          w.removeEventListener("drop", drop as EventListener);
          w.removeEventListener("dragend", end);
        } catch {
          // the frame is gone
        }
      };
    };
    const detachPage = attach(window);
    let detachFrame: (() => void) | undefined;
    const iframe = frame.current;
    const attachFrame = () => {
      detachFrame?.();
      detachFrame = undefined;
      try {
        if (iframe?.contentWindow) detachFrame = attach(iframe.contentWindow);
      } catch {
        // not same-origin: nothing to listen to
      }
    };
    attachFrame();
    iframe?.addEventListener("load", attachFrame);
    return () => {
      iframe?.removeEventListener("load", attachFrame);
      detachFrame?.();
      detachPage();
    };
  }, [frame, frameKey]);

  // Hide when the drag has gone quiet.
  useEffect(() => {
    if (!over) return;
    const t = window.setInterval(() => {
      if (Date.now() - lastSeen.current > STALE_MS) setOver(false);
    }, 200);
    // Neither fires during a drag, so one means the drag is over.
    const hide = () => setOver(false);
    window.addEventListener("pointerdown", hide);
    window.addEventListener("mousemove", hide);
    return () => {
      window.clearInterval(t);
      window.removeEventListener("pointerdown", hide);
      window.removeEventListener("mousemove", hide);
    };
  }, [over]);

  if (!over) return null;
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
        setOver(false);
        upload(e.dataTransfer);
      }}
    >
      Drop to copy into {desktop}
    </div>
  );
}

async function send(desktop: string, file: File): Promise<void> {
  const toast = notifyLive(`Copying ${file.name}…`);
  try {
    const r = await uploadFile(desktop, file, (f) => toast.set(`Copying ${file.name}… ${Math.round(f * 100)}%`));
    toast.finish(`Copied ${file.name} to ${r.path.split("/").pop()} (${bytes(r.bytes)})`);
  } catch (e) {
    toast.finish(`${file.name}: ${errorMessage(e)}`, { error: true });
  }
}
