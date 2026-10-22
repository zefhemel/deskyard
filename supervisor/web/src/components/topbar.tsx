import type { ComponentChildren } from "preact";
import { useState } from "preact/hooks";
import { useLocation } from "preact-iso";
import { api } from "../api.ts";
import { useLive } from "../live.tsx";
import { gb } from "../lib/format.ts";
import { ChangePasswordDialog } from "../dialogs/change-password.tsx";
import { Avatar } from "./avatar.tsx";
import { Logo } from "./logo.tsx";
import { Menu } from "./menu.tsx";
import { cx, Meter } from "./ui.tsx";

const nav = [
  { href: "/", label: "Desktops", match: (p: string) => p === "/" || p.startsWith("/desktops/") },
  { href: "/images", label: "Images", match: (p: string) => p === "/images" },
  { href: "/accounts", label: "Accounts", match: (p: string) => p === "/accounts" },
];

export function TopBar() {
  const { me, host } = useLive();
  const { path } = useLocation();
  const [changing, setChanging] = useState(false);

  const signOut = () => {
    api.logout().finally(() => {
      location.href = "/login";
    });
  };

  return (
    <div class="flex items-center gap-6 border-b border-line bg-surface px-8 py-3.5 max-[900px]:flex-wrap max-[900px]:gap-x-4 max-[900px]:gap-y-2 max-[900px]:px-4 max-[900px]:py-3">
      <a href="/" class="flex items-center gap-2 text-[17px] font-bold tracking-[-0.01em] whitespace-nowrap text-ink no-underline">
        <Logo size={24} />
        Deskyard
      </a>
      {me.admin && (
        <nav class="ml-3 flex gap-1 max-[900px]:order-last max-[900px]:ml-0 max-[900px]:w-full">
          {nav.map((n) => (
            <a
              key={n.href}
              href={n.href}
              class={cx(
                "rounded-[7px] px-[11px] py-1.5 text-sm font-semibold no-underline",
                n.match(path) ? "bg-hairline text-ink" : "text-muted hover:text-ink",
              )}
            >
              {n.label}
            </a>
          ))}
        </nav>
      )}
      <div class="ml-auto flex items-center gap-6 text-sm text-muted">
        {me.admin && host && (
          <>
            <span class="whitespace-nowrap max-sm:hidden">
              <strong class="font-semibold text-ink">{host.running}</strong> of {host.total} running
            </span>
            <span class="whitespace-nowrap max-[900px]:hidden">
              Memory <strong class="font-semibold text-ink">{gb(host.memUsed)}</strong> of {gb(host.memBytes)} GB
            </span>
            <Meter class="w-24 max-[900px]:hidden" percent={host.memBytes ? (100 * host.memUsed) / host.memBytes : 0} />
          </>
        )}
        <Menu
          items={[
            { label: "Change password", onSelect: () => setChanging(true) },
            { label: "Sign out", onSelect: signOut, separated: true },
          ]}
          trigger={({ toggle, open }) => (
            <button
              type="button"
              aria-haspopup="menu"
              aria-expanded={open}
              onClick={toggle}
              class="inline-flex cursor-pointer items-center gap-2 rounded-[7px] border-0 bg-transparent px-1.5 py-1 text-sm font-semibold text-ink hover:bg-hairline"
            >
              <Avatar name={me.username} />
              {me.username}
              {me.admin && <span class="font-medium text-muted max-sm:hidden">admin</span>}
            </button>
          )}
        />
      </div>
      {changing && <ChangePasswordDialog onClose={() => setChanging(false)} />}
    </div>
  );
}

/** The page frame for everything except the desktop page and login. */
export function Page({ children }: { children: ComponentChildren }) {
  return (
    <>
      <TopBar />
      <main class="mx-auto max-w-[1440px] px-8 pt-7 pb-12 max-[900px]:px-4 max-[900px]:pt-5 max-[900px]:pb-10">{children}</main>
    </>
  );
}

export function PageHead({ title, sub, children }: { title: string; sub?: ComponentChildren; children?: ComponentChildren }) {
  return (
    <div class="mb-[22px] flex flex-wrap items-baseline gap-x-4 gap-y-2">
      <h1 class="text-[26px] leading-tight font-bold tracking-[-0.015em]">{title}</h1>
      {sub && <span class="text-sm text-muted">{sub}</span>}
      {children && <div class="ml-auto flex gap-2 self-center">{children}</div>}
    </div>
  );
}
