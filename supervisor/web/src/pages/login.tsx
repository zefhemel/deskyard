import { useState } from "preact/hooks";
import { api, ApiError } from "../api.ts";
import { Logo } from "../components/logo.tsx";
import { Button, Field } from "../components/ui.tsx";

/** Only paths on this origin: never send someone elsewhere after signing in. */
function safeNext(next: string | null): string {
  if (!next || !next.startsWith("/") || next.startsWith("//") || next.startsWith("/\\")) return "/";
  return next;
}

/** A small drawn screen: a window on a desktop, or a dark (stopped) one. */
function Mini({ dark, wide }: { dark?: boolean; wide?: boolean }) {
  return (
    <div class="flex-1 rounded-[7px] bg-bezel p-1 shadow-[0_10px_24px_-14px_rgba(23,34,49,.55)]">
      <div class="relative aspect-[16/10] overflow-hidden rounded-[3px] bg-glass">
        {!dark && (
          <div class="absolute inset-0 bg-[#e9edf2]">
            <div class="h-[7%] bg-[#d5dbe3]" />
            <div class={`absolute top-[16%] left-[8%] rounded-sm bg-white shadow-sm ${wide ? "h-[66%] w-[84%]" : "h-[70%] w-[62%]"}`}>
              <div class="mx-[8%] mt-[10%] h-[5%] w-1/2 rounded-sm bg-[#c7ced8]" />
              <div class="mx-[8%] mt-[6%] h-[4%] w-3/4 rounded-sm bg-[#dfe3e9]" />
              <div class="mx-[8%] mt-[4%] h-[4%] w-2/3 rounded-sm bg-[#dfe3e9]" />
              {wide && <div class="absolute right-[6%] bottom-[8%] h-[38%] w-[44%] rounded-sm bg-[#1b2430]" />}
            </div>
            <div class="absolute bottom-0 h-[6%] w-full bg-[#2c3644]" />
          </div>
        )}
      </div>
    </div>
  );
}

export function LoginPage() {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const submit = async (e: Event) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await api.login(username, password);
      // A full load, so every page starts from a fresh session.
      location.href = safeNext(new URLSearchParams(location.search).get("next"));
    } catch (err) {
      if (err instanceof ApiError && err.status === 429) setError("Too many attempts, wait a few seconds.");
      else if (err instanceof ApiError && err.status === 401) setError("Wrong username or password.");
      else setError(err instanceof Error ? err.message : String(err));
      setBusy(false);
    }
  };

  return (
    <div class="grid min-h-screen place-items-center p-6">
      <form class="w-full max-w-[380px]" method="post" onSubmit={submit}>
        <div class="mb-[30px] flex gap-2.5" aria-hidden="true">
          <Mini />
          <Mini wide />
          <Mini dark />
        </div>
        <span class="mb-[26px] flex items-center gap-2 text-[19px] font-bold tracking-[-0.01em]">
          <Logo size={26} />
          Deskyard
        </span>
        <h1 class="mb-1.5 text-[26px] leading-tight font-bold tracking-[-0.015em]">Sign in</h1>
        <p class="mb-6 text-muted">to use the desktops on this machine.</p>
        <Field label="Username" htmlFor="u">
          <input
            id="u"
            name="username"
            type="text"
            autocomplete="username"
            autocapitalize="none"
            spellcheck={false}
            autofocus
            required
            value={username}
            onInput={(e) => setUsername(e.currentTarget.value)}
          />
        </Field>
        <Field label="Password" htmlFor="p">
          <input
            id="p"
            name="password"
            type="password"
            autocomplete="current-password"
            required
            value={password}
            onInput={(e) => setPassword(e.currentTarget.value)}
          />
        </Field>
        {error && (
          <p role="alert" class="-mt-1.5 mb-3.5 text-sm text-danger">
            {error}
          </p>
        )}
        <Button type="submit" variant="primary" class="mt-1 h-10! w-full text-[15px]!" disabled={busy}>
          {busy ? "Signing in…" : "Sign in"}
        </Button>
      </form>
    </div>
  );
}
