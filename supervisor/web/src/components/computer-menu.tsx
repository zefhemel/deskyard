// The ⋯ menu of a computer (tile, computer page): Restart, Stop, and for
// admins Settings and Remove….
import { useState } from "preact/hooks";
import { useLocation } from "preact-iso";
import { api, type Computer } from "../api.ts";
import { useLive } from "../live.tsx";
import { RemoveDialog } from "../dialogs/remove-computer.tsx";
import { Menu, type MenuItem } from "./menu.tsx";
import { notifyError } from "./toast.tsx";
import { IconButton, MORE } from "./ui.tsx";

export function power(name: string, action: "start" | "stop" | "restart"): void {
  api.power(name, action).catch(notifyError);
}

export function ComputerMenu({ computer: c, power: withPower = true, onRemoved }: { computer: Computer; power?: boolean; onRemoved?: () => void }) {
  const { me } = useLive();
  const { route } = useLocation();
  const [removing, setRemoving] = useState(false);

  const items: MenuItem[] = [];
  if (withPower && c.state === "running") {
    items.push({ label: "Restart", onSelect: () => power(c.name, "restart") });
    items.push({ label: "Stop", onSelect: () => power(c.name, "stop") });
  }
  if (withPower && c.state === "stopped") items.push({ label: "Start", onSelect: () => power(c.name, "start") });
  if (me.admin) {
    items.push({ label: "Settings", onSelect: () => route(`/computers/${c.name}?tab=settings`) });
    items.push({ label: "Remove…", danger: true, separated: true, onSelect: () => setRemoving(true) });
  }
  if (!items.length) return null;

  return (
    <>
      <Menu
        items={items}
        trigger={({ toggle, open }) => (
          <IconButton aria-label="More actions" aria-haspopup="menu" aria-expanded={open} onClick={toggle}>
            {MORE}
          </IconButton>
        )}
      />
      {removing && <RemoveDialog computer={c} onClose={() => setRemoving(false)} onRemoved={onRemoved} />}
    </>
  );
}
