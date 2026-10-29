import type { RefObject } from "preact";
import { useEffect, useRef, useState } from "preact/hooks";
import { uploadFile } from "../api.ts";
import { copyText } from "../lib/clipboard.ts";
import { notify } from "../components/toast.tsx";
import { Button, Dialog, ErrorText, Field, errorMessage } from "../components/ui.tsx";

/** Screenshot names remain useful when several are saved into Desktop. */
export function imageName(file: File, now = new Date()): string {
  if (file.name && !/^(image|blob|pasted[- ]image)(\.[a-z0-9]+)?$/i.test(file.name)) return file.name;
  const extensions: Record<string, string> = { "image/png": "png", "image/jpeg": "jpg", "image/webp": "webp", "image/gif": "gif", "image/tiff": "tiff", "image/bmp": "bmp", "image/svg+xml": "svg", "image/avif": "avif", "image/heic": "heic", "image/heif": "heif" };
  return `screenshot-${now.toISOString().replace(/[:.]/g, "-")}.${extensions[file.type] ?? file.name.split(".").at(-1)?.match(/^[a-z0-9]{1,8}$/i)?.[0] ?? "img"}`;
}

export function validImageName(name: string): boolean {
  return name.length > 0 && name !== "." && name !== ".." && !/[\/\x00]/.test(name) && new TextEncoder().encode(name).length <= 200;
}

export function validImagePath(value: string): boolean {
  return (value.startsWith("~/") || value.startsWith("/")) && !value.includes("\x00") && validImageName(value.split("/").at(-1) ?? "");
}

/** Listen on the page (terminal tabs) or just the desktop iframe. Capture
 * image pastes before xterm/Selkies consume them, without changing text paste. */
export function ImagePaste({ computer, frame, frameKey }: { computer: string; frame?: RefObject<HTMLIFrameElement | null>; frameKey?: number }) {
  const [file, setFile] = useState<File | null>(null);
  const occupied = useRef(false);
  useEffect(() => {
    const paste = (e: ClipboardEvent) => {
      const target = e.target as Element | null;
      // Form fields and dialogs retain their own clipboard behavior.
      if (!frame && target?.closest?.('[role="dialog"], input, [contenteditable="true"], textarea:not(.xterm-helper-textarea)')) return;
      const item = Array.from(e.clipboardData?.items ?? []).find((item) => item.kind === "file" && item.type.startsWith("image/"));
      const image = item?.getAsFile();
      if (!image) return;
      e.preventDefault();
      e.stopImmediatePropagation();
      if (occupied.current) return;
      occupied.current = true;
      setFile(image);
    };
    if (!frame) {
      window.addEventListener("paste", paste, true);
      return () => window.removeEventListener("paste", paste, true);
    }
    const iframe = frame.current;
    let attached: Window | null = null;
    const attach = () => {
      attached?.removeEventListener("paste", paste, true);
      attached = null;
      try {
        if (iframe?.contentWindow?.document) {
          attached = iframe.contentWindow;
          attached.addEventListener("paste", paste, true);
        }
      } catch { /* Only same-origin desktop frames are accessible. */ }
    };
    attach();
    iframe?.addEventListener("load", attach);
    return () => {
      iframe?.removeEventListener("load", attach);
      attached?.removeEventListener("paste", paste, true);
    };
  }, [computer, frame, frameKey]);

  if (!file) return null;
  return <ImageUploadDialog computer={computer} file={file} onClose={() => {
    occupied.current = false;
    setFile(null);
  }} />;
}

function ImageUploadDialog({ computer, file, onClose }: { computer: string; file: File; onClose: () => void }) {
  const [name, setName] = useState(() => `~/Desktop/${imageName(file)}`);
  const [progress, setProgress] = useState<number | null>(null);
  const [error, setError] = useState<string | null>(null);
  const busy = useRef(false);
  const close = () => { if (!busy.current) onClose(); };
  const upload = async () => {
    if (busy.current || !validImagePath(name)) return;
    busy.current = true;
    setProgress(0);
    setError(null);
    try {
      const result = await uploadFile(computer, file, setProgress, name);
      const copied = await copyText(result.path);
      notify(`Saved image to ${result.path}${copied ? " · Path copied" : " · Could not copy path"}`);
      onClose();
    } catch (error) {
      setError(errorMessage(error));
      setProgress(null);
    } finally {
      busy.current = false;
    }
  };
  return (
    <Dialog title="Save pasted image" intro="Choose where to save this image on the computer. The saved path will be copied to your clipboard." onClose={close}
      onSubmit={() => void upload()} footer={<>
        <Button onClick={close} disabled={progress !== null}>Cancel</Button>
        <Button type="submit" disabled={progress !== null || !validImagePath(name)}>
          {progress === null ? "Upload image" : `Uploading… ${Math.round(progress * 100)}%`}
        </Button>
      </>}>
      <Field label="File path" htmlFor="pasted-image-name" hint="An existing file is kept; a number is added to the new name if needed.">
        <input id="pasted-image-name" value={name} disabled={progress !== null} onInput={(e) => setName(e.currentTarget.value)} />
      </Field>
      <ErrorText error={error} />
    </Dialog>
  );
}
