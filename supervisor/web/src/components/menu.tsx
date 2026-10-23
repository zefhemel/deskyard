// A button that opens a small menu (the ⋯ menus, the account menu, the
// desktop bar's Keys and Quality).
import type { ComponentChildren } from "preact";
import { useEffect, useRef, useState } from "preact/hooks";
import { cx } from "./ui.tsx";

export interface MenuItem {
  label: ComponentChildren;
  onSelect: () => void;
  danger?: boolean;
  /** Draw a separator above this item. */
  separated?: boolean;
  checked?: boolean;
  /** Extra classes, e.g. to show an item on small screens only. */
  class?: string;
}

interface MenuProps {
  items: MenuItem[];
  /** Renders the trigger; `open` tells whether the menu is showing. */
  trigger: (props: { open: boolean; toggle: () => void }) => ComponentChildren;
  align?: "left" | "right";
  class?: string;
  /** Called after the menu closes (the desktop page refocuses its frame). */
  onClose?: () => void;
}

export function Menu({ items, trigger, align = "right", class: extra, onClose }: MenuProps) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  const close = () => {
    setOpen(false);
    onClose?.();
  };

  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) close();
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") close();
    };
    // Clicks into the desktop's frame don't reach this document; blur does.
    const onBlur = () => close();
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    window.addEventListener("blur", onBlur);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey);
      window.removeEventListener("blur", onBlur);
    };
  });

  return (
    <div ref={ref} class={cx("relative", extra)}>
      {trigger({ open, toggle: () => (open ? close() : setOpen(true)) })}
      {open && (
        <div
          role="menu"
          class={cx(
            "absolute top-[calc(100%+4px)] z-40 min-w-[180px] rounded-[9px] border border-line bg-surface p-[5px] text-ink shadow-[0_14px_30px_-12px_rgba(23,34,49,.35)]",
            align === "right" ? "right-0" : "left-0",
          )}
        >
          {items.map((item, i) => (
            <div key={i} class={item.class}>
              {item.separated && <hr class="mx-1 my-[5px] border-0 border-t border-line" />}
              <button
                type="button"
                role={item.checked === undefined ? "menuitem" : "menuitemcheckbox"}
                aria-checked={item.checked}
                class={cx(
                  "flex w-full cursor-pointer items-center gap-2 rounded-md border-0 bg-transparent px-2.5 py-2 text-left text-[15px] whitespace-nowrap hover:bg-[#e7ebf0]",
                  item.danger ? "text-danger" : "text-ink",
                )}
                onClick={() => {
                  setOpen(false);
                  item.onSelect();
                  onClose?.();
                }}
              >
                {item.checked !== undefined && (
                  <span aria-hidden="true" class="w-3.5 text-accent">
                    {item.checked ? "✓" : ""}
                  </span>
                )}
                <span>{item.label}</span>
              </button>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
