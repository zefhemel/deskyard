import { useState } from "preact/hooks";
import { api, type Account } from "../api.ts";
import { Avatar } from "../components/avatar.tsx";
import { Menu, type MenuItem } from "../components/menu.tsx";
import { useAction } from "../components/use-action.ts";
import { Page, PageHead } from "../components/topbar.tsx";
import { Button, card, Dialog, ErrorText, IconButton, MORE, Tag, td, th } from "../components/ui.tsx";
import { AccountDialog } from "../dialogs/account.tsx";
import { Expiry, LinkDialog } from "../dialogs/share.tsx";
import { useLive } from "../live.tsx";
import { ago } from "../lib/format.ts";
import { useFetch } from "../lib/use-fetch.ts";
import { DeviceList } from "./devices.tsx";

export function AccountsPage() {
  const { me } = useLive();
  const accounts = useFetch(() => api.accounts(), "accounts");
  const [editing, setEditing] = useState<Account | "new" | null>(null);
  const [deleting, setDeleting] = useState<Account | null>(null);
  const [devices, setDevices] = useState<Account | null>(null);
  const [link, setLink] = useState<{ username: string; link: string } | null>(null);

  const newLink = useAction((a: Account) => api.accountLink(a.username).then((r) => ({ username: a.username, link: r.link })), {
    toastErrors: true,
    onDone: (l) => {
      setLink(l);
      accounts.reload();
    },
  });

  return (
    <Page>
      <PageHead title="Accounts">
        <Button variant="primary" onClick={() => setEditing("new")}>
          New account
        </Button>
      </PageHead>
      <ErrorText error={accounts.error} />
      {accounts.data && (
        <div class={`overflow-x-auto ${card}`}>
          <table class="w-full border-collapse text-sm">
            <thead>
              <tr>
                <th class={th}>Account</th>
                <th class={th}>Can use</th>
                <th class={th}>Ends</th>
                <th class={th}>Last signed in</th>
                <th class={th} />
              </tr>
            </thead>
            <tbody>
              {accounts.data.map((a) => {
                const items: MenuItem[] = [
                  ...(a.kind === "token" ? [{ label: a.linkPending ? "New login link (replaces the unused one)" : "New login link", onSelect: () => void newLink.run(a) }] : []),
                  { label: "Signed-in devices…", onSelect: () => setDevices(a) },
                  { label: a.sharedBy ? "Revoke…" : "Delete…", danger: true, separated: true, onSelect: () => setDeleting(a) },
                ];
                return (
                  <tr key={a.username} class="last:[&>td]:border-b-0">
                    <td class={td}>
                      <span class="inline-flex items-center gap-2 font-semibold whitespace-nowrap">
                        <Avatar name={a.username} /> {a.username}
                        {a.admin && <Tag kind="admin">admin</Tag>}
                        {a.sharedBy && <Tag>share</Tag>}
                      </span>
                      <div class="mt-0.5 text-[13px] text-muted">
                        {[a.description, a.kind === "token" && !a.sharedBy ? "Signs in with a login link" : "", a.linkPending ? "Link not used yet" : ""]
                          .filter(Boolean)
                          .join(" · ")}
                      </div>
                    </td>
                    <td class={td}>
                      {a.admin ? (
                        <span class="text-muted">Every environment</span>
                      ) : a.grants.length ? (
                        <div class="flex flex-wrap gap-[5px]">
                          {a.grants.map((g) => (
                            <Tag key={g.environment}>
                              {g.environment}
                              {g.level === "observe" && <span class="font-medium text-muted"> · observe</span>}
                            </Tag>
                          ))}
                        </div>
                      ) : (
                        <span class="text-muted">No environments yet</span>
                      )}
                      {a.sharedBy && <div class="mt-1 text-[13px] text-muted">Shared by {a.sharedBy.username}</div>}
                    </td>
                    <td class={`${td} whitespace-nowrap`}>
                      <Expiry expires={a.expires} />
                    </td>
                    <td class={`${td} whitespace-nowrap`}>{a.username === me.username ? "Now" : ago(a.lastLogin)}</td>
                    <td class={`${td} text-right whitespace-nowrap`}>
                      <span class="inline-flex gap-1.5">
                        <Button size="sm" onClick={() => setEditing(a)}>
                          Edit
                        </Button>
                        <Menu
                          items={items}
                          trigger={({ toggle, open }) => (
                            <IconButton size="sm" aria-label={`More actions for ${a.username}`} aria-haspopup="menu" aria-expanded={open} onClick={toggle}>
                              {MORE}
                            </IconButton>
                          )}
                        />
                      </span>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
      {editing && (
        <AccountDialog
          account={editing === "new" ? null : editing}
          onClose={() => setEditing(null)}
          onSaved={(l) => {
            setEditing(null);
            if (l) setLink(l);
            accounts.reload();
          }}
        />
      )}
      {deleting && (
        <DeleteAccountDialog
          account={deleting}
          self={deleting.username === me.username}
          onClose={() => setDeleting(null)}
          onDeleted={() => {
            setDeleting(null);
            accounts.reload();
          }}
        />
      )}
      {devices && <AccountDevicesDialog account={devices} onClose={() => setDevices(null)} />}
      {link && <LinkDialog username={link.username} link={link.link} title={`A login link for ${link.username}`} onClose={() => setLink(null)} />}
    </Page>
  );
}

function AccountDevicesDialog({ account: a, onClose }: { account: Account; onClose: () => void }) {
  const devices = useFetch(() => api.accountCredentials(a.username), a.username);
  return (
    <Dialog
      title={`${a.username}'s signed-in devices`}
      intro="The deskyard command on each machine signed in as this account. Signing one out ends its open shells and connections at once."
      onClose={onClose}
      onSubmit={onClose}
      wide
      footer={
        <Button type="submit" variant="primary">
          Done
        </Button>
      }
    >
      <div class="mb-[18px]">
        <DeviceList devices={devices.data} error={devices.error} revoke={(id) => api.revokeAccountCredential(a.username, id)} onChanged={devices.reload} />
      </div>
    </Dialog>
  );
}

function DeleteAccountDialog({ account: a, self, onClose, onDeleted }: { account: Account; self: boolean; onClose: () => void; onDeleted: () => void }) {
  // A share is revoked the way its environment's Access tab revokes it.
  const remove = useAction(() => (a.sharedBy ? api.revokeShare(a.username) : api.deleteAccount(a.username)), {
    success: a.sharedBy ? `Revoked ${a.username}` : undefined,
    onDone: () => (self ? (location.href = "/login") : onDeleted()),
  });
  const { busy, error } = remove;
  const verb = a.sharedBy ? "Revoke" : "Delete";
  return (
    <Dialog
      title={`${verb} ${a.username}?`}
      intro={
        <>
          {a.username} is signed out everywhere: browsers and signed-in devices, with their open desktops, terminals and connections. Its login
          link stops working{a.sharedBy ? "" : ", and the shares it made end"}. Environments stay.
          {self && " This is your own account: you'll be signed out."}
        </>
      }
      onClose={onClose}
      onSubmit={() => void remove.run()}
      footer={
        <>
          <Button variant="quiet" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" variant="danger" disabled={busy}>
            {verb} {a.username}
          </Button>
        </>
      }
    >
      <ErrorText error={error} />
    </Dialog>
  );
}
