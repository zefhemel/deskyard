// The login link page, /link#dyl_…: says who the link signs in as and signs
// in when asked. The code is in the fragment, which browsers never send, so
// fetching the page (a link preview, a scanner) uses nothing up; nor does
// inspecting it. Only Sign in, a POST, uses the link.
import type { ComponentChildren } from "preact";
import { useEffect, useState } from "preact/hooks";
import { api, ApiError, type LinkInfo, type Me } from "../api.ts";
import { Logo } from "../components/logo.tsx";
import { Button, errorMessage } from "../components/ui.tsx";
import { grantsLine } from "../lib/grants.ts";
import { parseLinkFragment, safeNext } from "../lib/links.ts";
import { day } from "../lib/format.ts";

export const usedLink = "This link was already used or replaced. Ask the person who shared it for a new one.";

type State =
  | { kind: "loading" }
  | { kind: "ready"; info: LinkInfo; me: Me | null }
  | { kind: "used" }
  | { kind: "error"; text: string };

function failure(e: unknown): State {
  if (e instanceof ApiError && e.status === 410) return { kind: "used" };
  if (e instanceof ApiError && e.status === 429) return { kind: "error", text: "Too many attempts, wait a few seconds and reload this page." };
  return { kind: "error", text: errorMessage(e) };
}

/** The frame of the pages people reach from outside: the logo and a narrow column. */
export function Card({ children }: { children: ComponentChildren }) {
  return (
    <div class="grid min-h-screen place-items-center p-6">
      <div class="w-full max-w-[420px]">
        <span class="mb-[26px] flex items-center gap-2 text-[19px] font-bold tracking-[-0.01em]">
          <Logo size={26} />
          Deskyard
        </span>
        {children}
      </div>
    </div>
  );
}

export function LinkPage() {
  const [code] = useState(() => parseLinkFragment(location.hash));
  const [state, setState] = useState<State>(() => (code ? { kind: "loading" } : { kind: "used" }));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    document.title = "Sign in · Deskyard";
    if (!code) return;
    Promise.all([api.inspectLink(code), api.whoami().catch(() => null)]).then(
      ([info, me]) => setState({ kind: "ready", info, me }),
      (e: unknown) => setState(failure(e)),
    );
  }, [code]);

  const signIn = async () => {
    setBusy(true);
    setError(null);
    try {
      const r = await api.redeemLink(code);
      // A full load, so every page starts from the new session.
      location.href = safeNext(r.next);
    } catch (e) {
      const next = failure(e);
      if (next.kind === "used") setState(next);
      else setError(next.kind === "error" ? next.text : errorMessage(e));
      setBusy(false);
    }
  };

  if (state.kind === "loading") return <Card>{null}</Card>;
  if (state.kind === "used" || state.kind === "error") {
    return (
      <Card>
        <h1 class="mb-1.5 text-[26px] leading-tight font-bold tracking-[-0.015em]">{state.kind === "used" ? "This link doesn't work" : "Something went wrong"}</h1>
        <p role="alert" class="mb-6 text-muted">
          {state.kind === "used" ? usedLink : state.text}
        </p>
        <a href="/login">Sign in with a password instead</a>
      </Card>
    );
  }

  const { info, me } = state;
  const what = grantsLine(info.grants);
  return (
    <Card>
      <h1 class="mb-1.5 text-[26px] leading-tight font-bold tracking-[-0.015em]">
        Sign in as {info.username}
      </h1>
      <p class="mb-2 text-muted">
        to Deskyard{what && <>: {what}</>}
        {info.sharedBy && <>, shared by {info.sharedBy.username}</>}.
      </p>
      {info.description && <p class="mb-2 text-sm">{info.description}</p>}
      {info.expires && <p class="mb-2 text-sm text-muted">Access ends on {day(info.expires)}.</p>}
      {me && me.username !== info.username && (
        <p class="mt-4 mb-0 rounded-lg border border-[#ecd6ad] bg-[#fbf3e4] px-3 py-2.5 text-sm">
          This browser is signed in as <b>{me.username}</b>. Signing in switches it to {info.username}.
        </p>
      )}
      {error && (
        <p role="alert" class="mt-4 mb-0 text-sm text-danger">
          {error}
        </p>
      )}
      <Button variant="primary" class="mt-6 h-10! w-full text-[15px]!" disabled={busy} onClick={() => void signIn()}>
        {busy ? "Signing in…" : "Sign in"}
      </Button>
      <p class="mt-3 text-[13px] text-muted">The link works once: after this, this browser stays signed in.</p>
    </Card>
  );
}
