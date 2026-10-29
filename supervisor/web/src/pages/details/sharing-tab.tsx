import { useEffect, useState } from "preact/hooks";
import { api, type Computer, type Share } from "../../api.ts";
import { Avatar } from "../../components/avatar.tsx";
import { notify, notifyError } from "../../components/toast.tsx";
import { Button, Tag } from "../../components/ui.tsx";
import { Expiry, LinkDialog, ShareDialog } from "../../dialogs/share.tsx";
import { useLive } from "../../live.tsx";
import { levelLabel } from "../../lib/access.ts";
import { ago } from "../../lib/format.ts";
import { useFetch } from "../../lib/use-fetch.ts";

const th = "border-b border-line px-4 py-[11px] text-left text-[13px] font-semibold text-muted";
const td = "border-b border-hairline px-4 py-[13px] align-middle";

export function SharingTab({ computer: c }: { computer: Computer }) {
  const { me } = useLive();
  const shares = useFetch(() => api.shares(c.name), c.name);
  const [sharing, setSharing] = useState(false);
  const [link, setLink] = useState<{ username: string; link: string } | null>(null);

  // Who is connected follows the computer's viewers.
  const viewers = c.viewers.join(",");
  useEffect(() => {
    if (shares.data) shares.reload();
  }, [viewers]);

  return (
    <>
      <div class="flex items-center gap-4 border-b border-line px-5 py-4 max-sm:flex-col max-sm:items-start">
        <div class="flex-1">
          <h2 class="mt-0 mb-1 text-base font-semibold">Shares</h2>
          <p class="m-0 text-sm text-muted">
            {me.share
              ? `Everyone ${c.name} is shared with. Shares can't share it further.`
              : `Share ${c.name} with someone through a link that signs them in once: full access or observe only, for as long as you like. They show up among the viewers, and Revoke cuts them off at once.`}
          </p>
        </div>
        {!me.share && (
          <Button variant="primary" onClick={() => setSharing(true)}>
            Share
          </Button>
        )}
      </div>
      {shares.error && <p class="m-0 px-5 py-3.5 text-sm text-danger">{shares.error}</p>}
      {shares.data && shares.data.length === 0 && <p class="m-0 px-5 py-3.5 text-sm text-muted">{c.name} isn't shared with anyone.</p>}
      {shares.data && shares.data.length > 0 && (
        <div class="overflow-x-auto">
          <table class="w-full border-collapse text-sm">
            <thead>
              <tr>
                <th class={th}>Shared with</th>
                <th class={th}>Access</th>
                <th class={th}>Shared by</th>
                <th class={th}>Ends</th>
                <th class={th}>Last used</th>
                <th class={th} />
              </tr>
            </thead>
            <tbody>
              {shares.data.map((s) => (
                <ShareRow
                  key={s.username}
                  share={s}
                  canManage={me.admin || s.sharedBy.username === me.username}
                  onLink={(l) => setLink({ username: s.username, link: l })}
                  onChanged={shares.reload}
                />
              ))}
            </tbody>
          </table>
        </div>
      )}
      {sharing && (
        <ShareDialog
          computer={c.name}
          onClose={() => setSharing(false)}
          onShared={(username, l) => {
            setSharing(false);
            setLink({ username, link: l });
            shares.reload();
          }}
        />
      )}
      {link && <LinkDialog username={link.username} link={link.link} title={`${c.name} is shared with ${link.username}`} onClose={() => setLink(null)} />}
    </>
  );
}

function ShareRow({ share: s, canManage, onLink, onChanged }: { share: Share; canManage: boolean; onLink: (link: string) => void; onChanged: () => void }) {
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);

  const revoke = async () => {
    setBusy(true);
    try {
      await api.revokeShare(s.username);
      notify(`Revoked ${s.username}`);
      onChanged();
    } catch (e) {
      notifyError(e);
      setBusy(false);
    }
  };
  const newLink = async () => {
    setBusy(true);
    try {
      onLink((await api.newShareLink(s.username)).link);
      onChanged();
    } catch (e) {
      notifyError(e);
    } finally {
      setBusy(false);
    }
  };

  return (
    <tr class="last:[&>td]:border-b-0">
      <td class={td}>
        <span class="inline-flex items-center gap-2 font-semibold whitespace-nowrap">
          <Avatar name={s.username} /> {s.username}
          {s.connected && <Tag kind="ok">connected</Tag>}
        </span>
        {s.linkPending && <div class="mt-0.5 text-[13px] text-muted">Link not used yet</div>}
      </td>
      <td class={`${td} whitespace-nowrap`}>{levelLabel(s.level)}</td>
      <td class={`${td} whitespace-nowrap`}>{s.sharedBy.username}</td>
      <td class={`${td} whitespace-nowrap`}>
        <Expiry expires={s.expires} />
      </td>
      <td class={`${td} whitespace-nowrap`}>{ago(s.lastUsed)}</td>
      <td class={`${td} text-right whitespace-nowrap`}>
        {canManage &&
          (confirming ? (
            <span class="inline-flex gap-1.5">
              <Button variant="quiet" class="h-[30px]! px-[11px]!" onClick={() => setConfirming(false)}>
                Keep
              </Button>
              <Button variant="danger" class="h-[30px]! px-[11px]!" disabled={busy} onClick={() => void revoke()}>
                Revoke now
              </Button>
            </span>
          ) : (
            <span class="inline-flex gap-1.5">
              <Button
                class="h-[30px]! px-[11px]!"
                disabled={busy}
                title={s.linkPending ? "Replaces the unused link" : undefined}
                onClick={() => void newLink()}
              >
                New link
              </Button>
              <Button class="h-[30px]! px-[11px]!" onClick={() => setConfirming(true)}>
                Revoke
              </Button>
            </span>
          ))}
      </td>
    </tr>
  );
}
