import type { Modifiers } from "./keys.ts";

const keys = [
  ["c", "Ctrl+C", "Ctrl+C"],
  ["Escape", "Esc", "Escape"],
  ["Tab", "Tab", "Tab"],
  ["ArrowUp", "↑", "Arrow up"],
  ["ArrowDown", "↓", "Arrow down"],
  ["ArrowLeft", "←", "Arrow left"],
  ["ArrowRight", "→", "Arrow right"],
  ["d", "Ctrl+D", "Ctrl+D"],
  ["l", "Ctrl+L", "Ctrl+L"],
  ["History", "History", "Terminal history"],
  ["PageUp", "PgUp", "Page up"],
  ["PageDown", "PgDn", "Page down"],
  ["Home", "Home", "Home"],
  ["End", "End", "End"],
  ["Delete", "Del", "Delete"],
] as const;
export function KeyBar({
  modifiers,
  disabled,
  onModifier,
  onKey,
  onFocus,
}: {
  modifiers: Modifiers;
  disabled: boolean;
  onModifier: (key: keyof Modifiers) => void;
  onKey: (key: string, ctrl?: boolean) => void;
  onFocus: () => void;
}) {
  return (
    <div class="terminal-keybar" aria-label="Terminal keys">
      <div class="terminal-modifiers">
        <button
          type="button"
          aria-label="Control"
          aria-pressed={modifiers.ctrl}
          disabled={disabled}
          onPointerDown={(e) => e.preventDefault()}
          onClick={() => onModifier("ctrl")}
        >
          Ctrl
        </button>
        <button
          type="button"
          aria-label="Alt"
          aria-pressed={modifiers.alt}
          disabled={disabled}
          onPointerDown={(e) => e.preventDefault()}
          onClick={() => onModifier("alt")}
        >
          Alt
        </button>
        <button
          type="button"
          aria-label="Show keyboard"
          disabled={disabled}
          onPointerDown={(e) => e.preventDefault()}
          onClick={onFocus}
        >
          Keyboard
        </button>
        <span class="terminal-keyhint">History: PgUp/PgDn · Esc to return</span>
      </div>
      <div class="terminal-shortcuts">
        {keys.map(([key, text, label]) => (
          <button
            type="button"
            key={key}
            aria-label={label}
            disabled={disabled}
            onPointerDown={(e) => e.preventDefault()}
            onClick={() => onKey(key, label.startsWith("Ctrl+"))}
          >
            {text}
          </button>
        ))}
      </div>
    </div>
  );
}
