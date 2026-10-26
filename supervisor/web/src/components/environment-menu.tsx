// The ⋯ menu of an environment (tile, details page, desktop page): Restart, Stop, and for
// admins Settings, the page's own admin actions, and Remove….
import { useState } from "preact/hooks";
import { useLocation } from "preact-iso";
import { api, type Environment } from "../api.ts";
import { useLive } from "../live.tsx";
import { RemoveDialog } from "../dialogs/remove-environment.tsx";
import { Menu, type MenuItem } from "./menu.tsx";
import { notifyError } from "./toast.tsx";
import { IconButton, MORE } from "./ui.tsx";

export function power(name: string, action: "start" | "stop" | "restart"): void {
  api.power(name, action).catch(notifyError);
}

export function EnvironmentMenu({
  environment: c,
  power: withPower = true,
  adminItems = [],
  onRemoved,
}: {
  environment: Environment;
  power?: boolean;
  /** More admin actions, before Remove…. */
  adminItems?: MenuItem[];
  onRemoved?: () => void;
}) {
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
    items.push({ label: "Settings", onSelect: () => route(`/environments/${c.name}?tab=settings`) });
    items.push(...adminItems);
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
      {removing && <RemoveDialog environment={c} onClose={() => setRemoving(false)} onRemoved={onRemoved} />}
    </>
  );
}
