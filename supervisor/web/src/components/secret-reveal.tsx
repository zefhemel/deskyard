// A secret shown once (a recovery key, a WebDAV password, a login link),
// with Copy: copyText falls back to execCommand on plain HTTP, where the
// clipboard API is missing.
import { useState } from "preact/hooks";
import { copyText } from "../lib/clipboard.ts";
import { notify } from "./toast.tsx";
import { Button, cx } from "./ui.tsx";

/** Copies text, saying so in a toast; `what` names it ("the recovery key"). */
export async function copySecret(text: string, what: string): Promise<boolean> {
  const ok = await copyText(text);
  if (ok) notify(`Copied ${what}`);
  else notify("Couldn't copy; select it and copy it instead", { error: true });
  return ok;
}

/** Copy for a button that says "Copied" once it worked, instead of a toast. */
export function useCopied(text: string): { copied: boolean; copy: () => Promise<void> } {
  const [copied, setCopied] = useState(false);
  return { copied, copy: async () => setCopied(await copyText(text)) };
}

/**
 * The secret in a box with Copy, and what to do with it under it. `label`
 * names it for screen readers ("Recovery key"), `what` in the toast ("the recovery key").
 */
export function SecretReveal({ secret, label, what, note, class: extra }: { secret: string; label: string; what: string; note: string; class?: string }) {
  return (
    <div class={cx("rounded-lg border border-line p-3.5", extra)}>
      <div class="flex flex-wrap items-center gap-2">
        <code class="mono min-w-0 flex-1 break-all text-sm" aria-label={label}>
          {secret}
        </code>
        <Button onClick={() => void copySecret(secret, what)}>Copy</Button>
      </div>
      <p class="mt-2 text-[13px] text-muted">{note}</p>
    </div>
  );
}
