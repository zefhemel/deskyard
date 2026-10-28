import { useRef, useState } from "preact/hooks";
import { uploadFile } from "../api.ts";
import { copyText } from "../lib/clipboard.ts";
import { useEventListener, useFrameWindowListener } from "../lib/use-event-listener.ts";
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

/**
 * Pasting an image saves it into the environment. Listens on the page (the
 * bar and terminal tabs) and on the desktop frame's window while there is
 * one, capturing image pastes before xterm/Selkies consume them, without
 * changing text paste. On the page, form fields and dialogs keep their own
 * clipboard behavior.
 */
export function ImagePaste({ environment, frame }: { environment: string; frame: HTMLIFrameElement | null }) {
  const [file, setFile] = useState<File | null>(null);
  const occupied = useRef(false);
  const paste = (inFrame: boolean) => (e: ClipboardEvent) => {
    const target = e.target as Element | null;
    if (!inFrame && target?.closest?.('[role="dialog"], input, [contenteditable="true"], textarea:not(.xterm-helper-textarea)')) return;
    const item = Array.from(e.clipboardData?.items ?? []).find((item) => item.kind === "file" && item.type.startsWith("image/"));
    const image = item?.getAsFile();
    if (!image) return;
    e.preventDefault();
    e.stopImmediatePropagation();
    if (occupied.current) return;
    occupied.current = true;
    setFile(image);
  };
  useEventListener(window, "paste", paste(false), true);
  useFrameWindowListener(frame, "paste", paste(true), true);

  if (!file) return null;
  return <ImageUploadDialog environment={environment} file={file} onClose={() => {
    occupied.current = false;
    setFile(null);
  }} />;
}

function ImageUploadDialog({ environment, file, onClose }: { environment: string; file: File; onClose: () => void }) {
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
      const result = await uploadFile(environment, file, setProgress, name);
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
    <Dialog title="Save pasted image" intro="Choose where to save this image in the environment. The saved path will be copied to your clipboard." onClose={close}
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
