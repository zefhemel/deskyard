import type { ComponentChildren } from "preact";
import type { Environment } from "../api.ts";
import { statusKind } from "../lib/format.ts";
import { Status } from "./ui.tsx";

/** An environment as a linked pill: its status dot (when known), name and a tag. */
export function EnvironmentChip({ name, environment, href, children }: { name: string; environment?: Environment; href: string; children?: ComponentChildren }) {
  return (
    <a href={href} class="inline-flex items-center gap-2 rounded-full border border-line py-1 pr-1.5 pl-3 text-ink no-underline hover:border-accent">
      {environment && (
        <Status kind={statusKind(environment)}>
          <span class="sr-only">{environment.state}</span>
        </Status>
      )}
      <span class="font-semibold">{name}</span>
      {children}
    </a>
  );
}
