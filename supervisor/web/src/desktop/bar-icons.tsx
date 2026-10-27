// Small line icons for the desktop bar, drawn in the text colour.
import type { ComponentChildren } from "preact";

function Icon({ children, class: extra }: { children: ComponentChildren; class?: string }) {
  return (
    <svg width="17" height="17" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" class={extra}>
      {children}
    </svg>
  );
}

export const ShieldIcon = ({ class: extra }: { class?: string }) => (
  <Icon class={extra}>
    <path d="M8 1.75 2.75 3.6v4.1c0 3.1 2.2 5.5 5.25 6.55 3.05-1.05 5.25-3.45 5.25-6.55V3.6Z" />
  </Icon>
);
export const DisplayIcon = () => (
  <Icon>
    <rect x="1.75" y="2.5" width="12.5" height="8.5" rx="1.2" />
    <path d="M5.5 13.75h5M8 11v2.75" />
  </Icon>
);
export const ClipboardIcon = () => (
  <Icon>
    <rect x="3" y="2.75" width="10" height="11.5" rx="1.3" />
    <path d="M5.75 1.75h4.5v2h-4.5zM5.5 7.25h5M5.5 10h3.5" />
  </Icon>
);
export const KeyboardIcon = () => (
  <Icon>
    <rect x="1.25" y="3.75" width="13.5" height="8.5" rx="1.3" />
    <path d="M4 6.5h.01M6.5 6.5h.01M9 6.5h.01M11.5 6.5h.01M4.5 9.5h7" />
  </Icon>
);
export const TerminalIcon = () => (
  <Icon>
    <rect x="1.25" y="2.25" width="13.5" height="11.5" rx="1.3" />
    <path d="m4.25 6 2.25 2-2.25 2M8 10.5h3.5" />
  </Icon>
);
export const TextSizeIcon = () => (
  <Icon>
    <path d="M1.75 12.5 5 3.5l3.25 9M2.9 9.5h4.2M9.5 12.5l2.25-6 2.25 6M10.2 10.75h3.1" />
  </Icon>
);
