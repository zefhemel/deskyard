// The CLI login approval page, /cli-login#<id>: `deskyard login` opens it, and
// approving gives that CLI a credential for the signed-in account. Signed out,
// the API's 401 sends the browser to the login page and back here.
import { useEffect, useState } from "preact/hooks";
import { api, ApiError, type CliLoginRequest, type Me } from "../api.ts";
import { Button, errorMessage } from "../components/ui.tsx";
import { cliLoginId } from "../lib/links.ts";
import { ago } from "../lib/format.ts";
import { Card } from "./link.tsx";

const gone = "This login request expired or was already answered. Run deskyard login again.";

type State =
  | { kind: "loading" }
  | { kind: "ready"; request: CliLoginRequest; me: Me }
  | { kind: "approved" }
  | { kind: "denied" }
  | { kind: "gone"; text: string };

export function CliLoginPage() {
  const [id] = useState(() => cliLoginId(location.hash));
  const [state, setState] = useState<State>(() => (id ? { kind: "loading" } : { kind: "gone", text: "This page needs the address deskyard login printed." }));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    document.title = "Approve deskyard login · Deskyard";
    if (!id) return;
    // A 401 sends the browser to the login page, coming back here.
    Promise.all([api.me(), api.cliLogin(id)]).then(
      ([me, request]) => setState({ kind: "ready", request, me }),
      (e: unknown) => {
        if (e instanceof ApiError && e.status === 401) return;
        setState({ kind: "gone", text: e instanceof ApiError && e.status === 404 ? gone : errorMessage(e) });
      },
    );
  }, [id]);

  const answer = async (approve: boolean) => {
    setBusy(true);
    setError(null);
    try {
      await (approve ? api.approveCliLogin(id) : api.denyCliLogin(id));
      setState({ kind: approve ? "approved" : "denied" });
    } catch (e) {
      if (e instanceof ApiError && e.status === 404) setState({ kind: "gone", text: gone });
      else setError(errorMessage(e));
      setBusy(false);
    }
  };

  if (state.kind === "loading") return <Card>{null}</Card>;
  if (state.kind !== "ready") {
    const [title, text] =
      state.kind === "approved"
        ? ["Approved", "The deskyard command is signed in. Go back to your terminal."]
        : state.kind === "denied"
          ? ["Denied", "The deskyard command wasn't signed in. You can close this page."]
          : ["Nothing to approve", state.text];
    return (
      <Card>
        <h1 class="mb-1.5 text-[26px] leading-tight font-bold tracking-[-0.015em]">{title}</h1>
        <p role={state.kind === "gone" ? "alert" : undefined} class="mb-6 text-muted">
          {text}
        </p>
        <a href="/">Go to your environments</a>
      </Card>
    );
  }

  const { request: r, me } = state;
  return (
    <Card>
      <h1 class="mb-1.5 text-[26px] leading-tight font-bold tracking-[-0.015em]">Sign in the deskyard command?</h1>
      <p class="mb-5 text-muted">
        It will act as <b class="text-ink">{me.username}</b>, with your access, until you sign it out under Signed-in devices.
      </p>
      <dl class="mb-5 grid grid-cols-[max-content_1fr] gap-x-[18px] gap-y-[9px] text-sm">
        <dt class="text-muted">Client</dt>
        <dd class="m-0 [overflow-wrap:anywhere]">{r.client}</dd>
        <dt class="text-muted">From</dt>
        <dd class="mono m-0">{r.ip}</dd>
        <dt class="text-muted">Started</dt>
        <dd class="m-0">{ago(r.created)}</dd>
        <dt class="text-muted">Code</dt>
        <dd data-cli-code class="mono m-0 text-[22px] font-semibold tracking-[0.08em]">
          {r.code}
        </dd>
      </dl>
      <p class="mb-5 rounded-lg border border-[#eec3be] bg-[#fbeceb] px-3 py-2.5 text-sm">
        Only approve if you just ran <span class="mono">deskyard login</span> yourself, and the code matches the one in your terminal.
      </p>
      {error && (
        <p role="alert" class="mb-3.5 text-sm text-danger">
          {error}
        </p>
      )}
      <div class="flex gap-2.5">
        <Button variant="primary" class="h-10! flex-1 text-[15px]!" disabled={busy} onClick={() => void answer(true)}>
          Approve
        </Button>
        <Button class="h-10! flex-1 text-[15px]!" disabled={busy} onClick={() => void answer(false)}>
          Deny
        </Button>
      </div>
    </Card>
  );
}
