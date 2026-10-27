// Small building blocks in the mockups' style: buttons, dialogs, fields,
// segmented controls, tags, status dots and meters.
import type { ButtonHTMLAttributes, ComponentChildren } from "preact";
import { useEffect, useRef } from "preact/hooks";

export function cx(...parts: (string | false | null | undefined)[]): string {
  return parts.filter(Boolean).join(" ");
}

export type Variant = "default" | "primary" | "danger" | "quiet";

const variants: Record<Variant, string> = {
  default: "bg-surface border-line text-ink hover:bg-white",
  primary: "bg-accent border-accent text-white hover:brightness-110",
  danger: "bg-danger border-danger text-white hover:brightness-110",
  quiet: "bg-transparent border-transparent text-muted hover:text-ink",
};

export const btnBase =
  "inline-flex items-center justify-center gap-1.5 h-[34px] px-3.5 border rounded-[7px] font-semibold text-sm whitespace-nowrap cursor-pointer no-underline disabled:opacity-50 disabled:cursor-default";

export function btnClass(variant: Variant = "default", extra?: string): string {
  return cx(btnBase, variants[variant], extra);
}

type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & { variant?: Variant };

export function Button({ variant = "default", class: extra, type = "button", ...rest }: ButtonProps) {
  return <button type={type} class={btnClass(variant, typeof extra === "string" ? extra : undefined)} {...rest} />;
}

export function IconButton({ class: extra, ...rest }: ButtonHTMLAttributes<HTMLButtonElement>) {
  return (
    <button
      type="button"
      class={cx(btnBase, variants.default, "w-[34px] px-0! text-lg leading-none text-muted", typeof extra === "string" ? extra : undefined)}
      {...rest}
    />
  );
}

/** The ⋯ glyph used on every "More actions" button. */
export const MORE = "⋯";

interface DialogProps {
  title: ComponentChildren;
  intro?: ComponentChildren;
  onClose: () => void;
  onSubmit?: () => void;
  footer: ComponentChildren;
  children?: ComponentChildren;
  wide?: boolean;
}

export function Dialog({ title, intro, onClose, onSubmit, footer, children, wide }: DialogProps) {
  const ref = useRef<HTMLFormElement>(null);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    // Focus the first field, as a dialog should.
    const first = ref.current?.querySelector<HTMLElement>("input:not([disabled]):not(.hidden), textarea, select");
    first?.focus();
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);
  return (
    <div
      data-dialog-overlay
      class="fixed inset-0 z-50 flex items-start justify-center bg-[rgba(23,34,49,.42)] px-4 pt-[72px] pb-8 max-sm:pt-6 max-sm:pb-6"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div
        role="dialog"
        aria-modal="true"
        // A tall dialog keeps its title and buttons in view and scrolls its body.
        class={cx("flex max-h-full w-full flex-col rounded-xl bg-surface shadow-[0_30px_60px_-20px_rgba(10,16,24,.5)]", wide ? "max-w-[620px]" : "max-w-[520px]")}
      >
      <form
        ref={ref}
        class="flex min-h-0 flex-1 flex-col"
        onSubmit={(e) => {
          e.preventDefault();
          onSubmit?.();
        }}
      >
        <header class="flex-none px-[26px] pt-[22px] pb-1.5">
          <h2 class="text-xl leading-tight font-bold">{title}</h2>
          {intro && <p class="mt-1.5 text-muted">{intro}</p>}
        </header>
        <div class="min-h-0 flex-1 overflow-y-auto overscroll-contain px-[26px] pt-3.5 pb-1.5">{children}</div>
        <footer class="flex flex-none flex-wrap justify-end gap-2.5 px-[26px] pt-[18px] pb-[22px]">{footer}</footer>
      </form>
      </div>
    </div>
  );
}

export function Field({
  label,
  htmlFor,
  hint,
  hintOk,
  children,
}: {
  label?: ComponentChildren;
  htmlFor?: string;
  hint?: ComponentChildren;
  hintOk?: boolean;
  children: ComponentChildren;
}) {
  return (
    <div class="mb-[18px]">
      {label &&
        (htmlFor ? (
          <label for={htmlFor} class="mb-1.5 block text-sm font-semibold">
            {label}
          </label>
        ) : (
          <span class="mb-1.5 block text-sm font-semibold">{label}</span>
        ))}
      {children}
      {hint && <div class={cx("mt-1.5 text-[13px]", hintOk ? "text-run" : "text-muted")}>{hint}</div>}
    </div>
  );
}

/** A checkbox with a bold label and an explanation under it. */
export function Check({
  id,
  checked,
  disabled,
  onChange,
  title,
  children,
}: {
  id: string;
  checked: boolean;
  disabled?: boolean;
  onChange: (v: boolean) => void;
  title: ComponentChildren;
  children?: ComponentChildren;
}) {
  return (
    <div class="mb-[18px] flex items-start gap-2.5">
      <input type="checkbox" id={id} class="mt-[3px]" checked={checked} disabled={disabled} onChange={(e) => onChange(e.currentTarget.checked)} />
      <label for={id}>
        <b class="font-semibold">{title}</b>
        {children && <p class="mt-0.5 text-[13px] text-muted">{children}</p>}
      </label>
    </div>
  );
}

export interface SegOption<T> {
  value: T;
  label: string;
}

export function Seg<T>({ options, value, onChange, label }: { options: SegOption<T>[]; value: T; onChange: (v: T) => void; label?: string }) {
  return (
    <div role="radiogroup" aria-label={label} class="inline-flex max-w-full overflow-x-auto rounded-[7px] border border-line bg-white">
      {options.map((o) => (
        <button
          key={o.label}
          type="button"
          role="radio"
          aria-checked={o.value === value}
          class={cx(
            "h-[34px] cursor-pointer border-r border-line px-3.5 text-sm whitespace-nowrap last:border-r-0",
            o.value === value ? "bg-ink font-semibold text-white" : "text-ink",
          )}
          onClick={() => onChange(o.value)}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}

/** A bordered, scrolling list of checkboxes. */
export function Picks({ children }: { children: ComponentChildren }) {
  return <div class="max-h-[260px] overflow-auto rounded-lg border border-line bg-white">{children}</div>;
}

export function Pick({
  checked,
  disabled,
  onChange,
  children,
}: {
  checked: boolean;
  disabled?: boolean;
  onChange?: (v: boolean) => void;
  children: ComponentChildren;
}) {
  return (
    <label class="flex items-center gap-2.5 border-b border-hairline px-3 py-2 text-sm last:border-b-0">
      <input type="checkbox" checked={checked} disabled={disabled} onChange={(e) => onChange?.(e.currentTarget.checked)} />
      {children}
    </label>
  );
}

export type TagKind = "plain" | "admin" | "update" | "ok" | "danger";

const tagKinds: Record<TagKind, string> = {
  plain: "bg-hairline text-ink",
  admin: "bg-[#dfe8fb] text-accent",
  update: "bg-[#fbf0dc] text-[#8a5a12]",
  ok: "bg-[#dcf1e6] text-run",
  danger: "bg-[#fbe3e1] text-danger",
};

export function Tag({ kind = "plain", children }: { kind?: TagKind; children: ComponentChildren }) {
  return <span class={cx("inline-block rounded-full px-2 py-px text-[12.5px] font-semibold whitespace-nowrap", tagKinds[kind])}>{children}</span>;
}


const dots = { run: "bg-run", busy: "bg-busy", off: "bg-stop" };

export function Status({ kind, children, class: extra }: { kind: keyof typeof dots; children: ComponentChildren; class?: string }) {
  return (
    <span class={cx("inline-flex items-center gap-1.5 text-[13px] text-muted", extra)}>
      <span class={cx("h-2 w-2 flex-none rounded-full", dots[kind])} />
      {children}
    </span>
  );
}

export function Meter({ percent, hot, class: extra }: { percent: number; hot?: boolean; class?: string }) {
  const w = Math.max(0, Math.min(100, percent));
  return (
    <div class={cx("h-[5px] overflow-hidden rounded-[3px] bg-track", extra)}>
      <i class={cx("block h-full rounded-[3px]", hot ? "bg-busy" : "bg-ink")} style={{ width: `${w}%` }} />
    </div>
  );
}

export function Note({ danger, children }: { danger?: boolean; children: ComponentChildren }) {
  return (
    <div
      class={cx(
        "mb-[18px] rounded-lg border px-3 py-2.5 text-sm",
        danger ? "border-[#eec3be] bg-[#fbeceb]" : "border-[#ecd6ad] bg-[#fbf3e4]",
      )}
    >
      {children}
    </div>
  );
}

export function ErrorText({ error }: { error: string | null | undefined }) {
  if (!error) return null;
  return (
    <p role="alert" class="mb-3.5 text-sm text-danger">
      {error}
    </p>
  );
}

export function Progress({ value, class: extra }: { value: number | null; class?: string }) {
  return (
    <div class={cx("h-1.5 overflow-hidden rounded-[3px] bg-[#2c3644]", extra)}>
      {value === null ? (
        <i class="block h-full w-1/3 animate-[slide_1.4s_ease-in-out_infinite] bg-busy" />
      ) : (
        <i class="block h-full bg-busy transition-[width]" style={{ width: `${Math.round(value * 100)}%` }} />
      )}
    </div>
  );
}

export function errorMessage(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}
