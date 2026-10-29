import type { ComponentChildren } from "preact";
import type { Computer } from "../api.ts";
import { statusKind } from "../lib/format.ts";
import { Status } from "./ui.tsx";

/** A computer as a linked pill: its status dot (when known), name and a tag. */
export function ComputerChip({ name, computer, href, children }: { name: string; computer?: Computer; href: string; children?: ComponentChildren }) {
  return (
    <a href={href} class="inline-flex items-center gap-2 rounded-full border border-line py-1 pr-1.5 pl-3 text-ink no-underline hover:border-accent">
      {computer && (
        <Status kind={statusKind(computer)}>
          <span class="sr-only">{computer.state}</span>
        </Status>
      )}
      <span class="font-semibold">{name}</span>
      {children}
    </a>
  );
}
