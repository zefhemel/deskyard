import { useState } from "preact/hooks";
import { useLocation, useRoute } from "preact-iso";
import { api } from "../api.ts";
import { BackupsPanel } from "../components/backups-panel.tsx";
import { Page, useTitle } from "../components/topbar.tsx";
import { Button, ErrorText, Tag } from "../components/ui.tsx";
import { useFetch } from "../lib/use-fetch.ts";
import { BackupTag, DeleteVolume, VolumeCard } from "./volumes.tsx";

/** One volume: its folder, usage and grants, and its backups. */
export function VolumeDetailsPage() {
  const { params } = useRoute();
  const { route } = useLocation();
  const name = params.name ?? "";
  const list = useFetch(() => api.volumes(), "volumes");
  const envs = useFetch(() => api.computers(), "environments");
  const backup = useFetch(() => api.backup("volume", name), `backup-volume-${name}`);
  const [removing, setRemoving] = useState(false);
  const v = list.data?.volumes.find((x) => x.name === name);
  useTitle(name, "Storage");
  return (
    <Page>
      <div class="mb-2 text-sm">
        <a href="/volumes" class="text-muted no-underline hover:underline">
          Storage
        </a>
      </div>
      <ErrorText error={list.error} />
      {list.data && !v && <p class="text-muted">There's no volume {name}.</p>}
      {v && (
        <>
          <div class="mb-[22px] flex flex-wrap items-center gap-x-3.5 gap-y-2">
            <h1 class="text-[30px] leading-tight font-bold tracking-[-0.02em]">{v.name}</h1>
            {v.kind === "host" && <Tag>Host folder</Tag>}
            <BackupTag backup={backup.data} />
            <Button class="ml-auto" onClick={() => setRemoving(true)}>
              Delete
            </Button>
          </div>
          <VolumeCard volume={v} computers={envs.data?.environments} bare />
          <h2 class="mt-8 mb-3 text-xl font-bold">Backups</h2>
          <BackupsPanel
            kind="volume"
            name={v.name}
            hostPath={v.kind === "host" ? v.folder : undefined}
            stoppers={v.environments.filter((g) => envs.data?.environments.find((e) => e.name === g.name)?.state === "running").map((g) => g.name)}
          />
          {removing && <DeleteVolume volume={v} onClose={() => setRemoving(false)} onDeleted={() => route("/volumes")} />}
        </>
      )}
    </Page>
  );
}
