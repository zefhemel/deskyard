// Revoking a share: on the Access tab's list of shares and in the viewers
// popover, both asking again in place. The share ends at once, with its
// connections.
import type { ComponentChildren } from "preact";
import { api } from "../api.ts";
import { ConfirmInline } from "./ui.tsx";
import { useAction } from "./use-action.ts";

export function RevokeShare({
  username,
  compact,
  onRevoked,
  children,
}: {
  username: string;
  /** The popover's small text buttons. */
  compact?: boolean;
  onRevoked?: () => void;
  /** The row's other actions, hidden while it asks. */
  children?: ComponentChildren;
}) {
  const revoke = useAction(() => api.revokeShare(username), { success: `Revoked ${username}`, toastErrors: true, onDone: onRevoked });
  return (
    <ConfirmInline
      label="Revoke"
      confirm="Revoke now"
      cancel={compact ? "Cancel" : "Keep"}
      busy={revoke.busy}
      onConfirm={() => void revoke.run()}
      compact={compact}
      triggerLabel={compact ? `Revoke ${username}` : undefined}
      triggerTitle={compact ? `End ${username}'s share and cut them off` : undefined}
    >
      {children}
    </ConfirmInline>
  );
}
