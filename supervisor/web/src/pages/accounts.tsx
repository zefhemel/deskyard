import { useState } from "preact/hooks";
import { api, type Account } from "../api.ts";
import { Avatar } from "../components/avatar.tsx";
import { Menu } from "../components/menu.tsx";
import { Page, PageHead } from "../components/topbar.tsx";
import { Button, Dialog, ErrorText, errorMessage, IconButton, MORE, Tag } from "../components/ui.tsx";
import { AccountDialog } from "../dialogs/account.tsx";
import { useLive } from "../live.tsx";
import { ago } from "../lib/format.ts";
import { useFetch } from "../lib/use-fetch.ts";

const th = "border-b border-line px-4 py-[11px] text-left text-[13px] font-semibold text-muted";
const td = "border-b border-hairline px-4 py-[13px] align-middle";

export function AccountsPage() {
  const { me } = useLive();
  const accounts = useFetch(() => api.accounts(), "accounts");
  const [editing, setEditing] = useState<Account | "new" | null>(null);
  const [deleting, setDeleting] = useState<Account | null>(null);

  return (
    <Page>
      <PageHead title="Accounts">
        <Button variant="primary" onClick={() => setEditing("new")}>
          New account
        </Button>
      </PageHead>
      <ErrorText error={accounts.error} />
      {accounts.data && (
        <div class="overflow-x-auto rounded-[10px] border border-line bg-surface">
          <table class="w-full border-collapse text-sm">
            <thead>
              <tr>
                <th class={th}>Account</th>
                <th class={th}>Can use</th>
                <th class={th}>Last signed in</th>
                <th class={th} />
              </tr>
            </thead>
            <tbody>
              {accounts.data.map((a) => (
                <tr key={a.username} class="last:[&>td]:border-b-0">
                  <td class={td}>
                    <span class="inline-flex items-center gap-2 font-semibold whitespace-nowrap">
                      <Avatar name={a.username} /> {a.username}
                      {a.admin && <Tag kind="admin">admin</Tag>}
                    </span>
                  </td>
                  <td class={td}>
                    {a.admin ? (
                      <span class="text-muted">Every environment</span>
                    ) : a.environments.length ? (
                      <div class="flex flex-wrap gap-[5px]">
                        {a.environments.map((c) => (
                          <Tag key={c}>{c}</Tag>
                        ))}
                      </div>
                    ) : (
                      <span class="text-muted">No environments yet</span>
                    )}
                  </td>
                  <td class={`${td} whitespace-nowrap`}>{a.username === me.username ? "Now" : ago(a.lastLogin)}</td>
                  <td class={`${td} text-right whitespace-nowrap`}>
                    <span class="inline-flex gap-1.5">
                      <Button class="h-[30px]! px-[11px]!" onClick={() => setEditing(a)}>
                        Edit
                      </Button>
                      <Menu
                        items={[{ label: "Delete…", danger: true, onSelect: () => setDeleting(a) }]}
                        trigger={({ toggle, open }) => (
                          <IconButton class="h-[30px]! w-[30px]!" aria-label={`More actions for ${a.username}`} aria-haspopup="menu" aria-expanded={open} onClick={toggle}>
                            {MORE}
                          </IconButton>
                        )}
                      />
                    </span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {editing && (
        <AccountDialog
          account={editing === "new" ? null : editing}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null);
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
    </Page>
  );
}

function DeleteAccountDialog({ account: a, self, onClose, onDeleted }: { account: Account; self: boolean; onClose: () => void; onDeleted: () => void }) {
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const submit = async () => {
    setBusy(true);
    try {
      await api.deleteAccount(a.username);
      if (self) location.href = "/login";
      else onDeleted();
    } catch (e) {
      setError(errorMessage(e));
      setBusy(false);
    }
  };
  return (
    <Dialog
      title={`Delete ${a.username}?`}
      intro={
        <>
          {a.username} is signed out everywhere, and their access tokens are revoked. Their environments stay.
          {self && " This is your own account: you'll be signed out."}
        </>
      }
      onClose={onClose}
      onSubmit={submit}
      footer={
        <>
          <Button variant="quiet" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" variant="danger" disabled={busy}>
            Delete {a.username}
          </Button>
        </>
      }
    >
      <ErrorText error={error} />
    </Dialog>
  );
}
