import { useState } from "preact/hooks";
import { api, type Desktop, type CreatedToken, type Token } from "../../api.ts";
import { notifyError } from "../../components/toast.tsx";
import { Button, Tag } from "../../components/ui.tsx";
import { NewTokenDialog, TokenCreatedDialog } from "../../dialogs/tokens.tsx";
import { useLive } from "../../live.tsx";
import { ago, day, until } from "../../lib/format.ts";
import { useFetch } from "../../lib/use-fetch.ts";

const th = "border-b border-line px-4 py-[11px] text-left text-[13px] font-semibold text-muted";
const td = "border-b border-hairline px-4 py-[13px] align-middle";

export function AgentsTab({ desktop: c }: { desktop: Desktop }) {
  const { me } = useLive();
  const tokens = useFetch(() => api.tokens(c.name), c.name);
  const [creating, setCreating] = useState(false);
  const [created, setCreated] = useState<CreatedToken | null>(null);

  return (
    <>
      <div class="flex items-center gap-4 border-b border-line px-4 py-3.5 max-sm:flex-col max-sm:items-start">
        <p class="m-0 flex-1 text-sm text-muted">
          Access {c.name} from your terminal through the <span class="mono">deskyard</span> command with one of these tokens: a shell, files,
          the browser and the desktop, as <span class="mono">user</span>. Nothing else.
        </p>
        <Button variant="primary" onClick={() => setCreating(true)}>
          New token
        </Button>
      </div>
      {tokens.error && <p class="m-0 px-4 py-3.5 text-sm text-danger">{tokens.error}</p>}
      {tokens.data && tokens.data.length === 0 && <p class="m-0 px-4 py-3.5 text-sm text-muted">No access tokens yet.</p>}
      {tokens.data && tokens.data.length > 0 && (
        <div class="overflow-x-auto">
          <table class="w-full border-collapse text-sm">
            <thead>
              <tr>
                <th class={th}>Token</th>
                <th class={th}>Created</th>
                <th class={th}>Expires</th>
                <th class={th}>Last used</th>
                <th class={th} />
              </tr>
            </thead>
            <tbody>
              {tokens.data.map((t) => (
                <TokenRow
                  key={t.id}
                  desktop={c.name}
                  token={t}
                  canRevoke={me.admin || t.createdBy === me.username}
                  onChanged={tokens.reload}
                />
              ))}
            </tbody>
          </table>
        </div>
      )}
      {creating && (
        <NewTokenDialog
          desktop={c.name}
          onClose={() => setCreating(false)}
          onCreated={(t) => {
            setCreating(false);
            setCreated(t);
            tokens.reload();
          }}
        />
      )}
      {created && <TokenCreatedDialog desktop={c.name} token={created} onClose={() => setCreated(null)} />}
    </>
  );
}

function TokenRow({ desktop, token: t, canRevoke, onChanged }: { desktop: string; token: Token; canRevoke: boolean; onChanged: () => void }) {
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);
  const muted = t.expired ? "text-muted" : "";

  const revoke = async () => {
    setBusy(true);
    try {
      await api.deleteToken(desktop, t.id);
      onChanged();
    } catch (e) {
      notifyError(e);
      setBusy(false);
    }
  };

  return (
    <tr class="last:[&>td]:border-b-0">
      <td class={`${td} ${muted}`}>
        <div class="font-semibold">{t.name}</div>
        <div class="mono mt-0.5 text-[13px] text-muted">dyt_{t.id.slice(0, 4)}…</div>
      </td>
      <td class={`${td} ${muted} whitespace-nowrap`}>
        {t.createdBy}, {day(t.created)}
      </td>
      <td class={`${td} ${muted}`}>
        {t.expired ? (
          <Tag kind="update">Expired{t.expires ? ` ${day(t.expires)}` : ""}</Tag>
        ) : t.expires ? (
          <>
            {day(t.expires)} <span class="text-muted">({until(t.expires)})</span>
          </>
        ) : (
          "Never"
        )}
      </td>
      <td class={`${td} ${muted} whitespace-nowrap`}>{ago(t.lastUsed)}</td>
      <td class={`${td} text-right whitespace-nowrap`}>
        {canRevoke &&
          (confirming ? (
            <span class="inline-flex gap-1.5">
              <Button variant="quiet" class="h-[30px]! px-[11px]!" onClick={() => setConfirming(false)}>
                Keep
              </Button>
              <Button variant="danger" class="h-[30px]! px-[11px]!" disabled={busy} onClick={revoke}>
                {t.expired ? "Delete" : "Revoke now"}
              </Button>
            </span>
          ) : (
            <Button class="h-[30px]! px-[11px]!" onClick={() => setConfirming(true)}>
              {t.expired ? "Delete" : "Revoke"}
            </Button>
          ))}
      </td>
    </tr>
  );
}
