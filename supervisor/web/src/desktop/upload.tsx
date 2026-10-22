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

/**
 * Dropping files anywhere on the desktop page copies them into the desktop's
 * ~/Desktop. The page listens for the drag; while one is in progress an
 * overlay covers the screen (the iframe would swallow the drag events) and
 * takes the drop.
 */
export function DropZone({ desktop, onDone }: { desktop: string; onDone?: () => void }) {
  const [over, setOver] = useState(false);
  const depth = useRef(0);
  const overlay = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const enter = (e: DragEvent) => {
      if (!hasFiles(e)) return;
      depth.current++;
      setOver(true);
    };
    const leave = (e: DragEvent) => {
      if (!hasFiles(e)) return;
      depth.current = Math.max(0, depth.current - 1);
      if (depth.current === 0) setOver(false);
    };
    const reset = () => {
      depth.current = 0;
      setOver(false);
    };
    const overPage = (e: DragEvent) => {
      if (hasFiles(e)) e.preventDefault(); // allow the drop (and stop the browser opening the file)
    };
    window.addEventListener("dragenter", enter);
    window.addEventListener("dragleave", leave);
    window.addEventListener("dragover", overPage);
    window.addEventListener("drop", reset);
    window.addEventListener("dragend", reset);
    return () => {
      window.removeEventListener("dragenter", enter);
      window.removeEventListener("dragleave", leave);
      window.removeEventListener("dragover", overPage);
      window.removeEventListener("drop", reset);
      window.removeEventListener("dragend", reset);
    };
  }, []);

  const onDrop = (e: DragEvent) => {
    e.preventDefault();
    depth.current = 0;
    setOver(false);
    const dt = e.dataTransfer;
    if (!dt) return;
    const items = Array.from(dt.items);
    const files = Array.from(dt.files);
    const toSend: File[] = [];
    files.forEach((f, i) => {
      if (isFolder(items[i], f)) notify(`${f.name} is a folder: drop files, not folders`, { error: true, ms: 6000 });
      else toSend.push(f);
    });
    for (const f of toSend) void send(desktop, f).then(onDone);
  };

  if (!over) return null;
  return (
    <div
      ref={overlay}
      data-testid="drop-overlay"
      class="fixed inset-x-0 top-[46px] bottom-0 z-40 flex items-center justify-center border-2 border-dashed border-white/70 bg-[rgba(15,21,29,.72)] text-xl font-bold text-white"
      onDragOver={(e) => {
        e.preventDefault();
        if (e.dataTransfer) e.dataTransfer.dropEffect = "copy";
      }}
      onDrop={onDrop}
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
