import { useState } from "preact/hooks";
import { useLocation, useRoute } from "preact-iso";
import { api } from "../api.ts";
import { BackupsPanel } from "../components/backups-panel.tsx";
import { Page } from "../components/topbar.tsx";
import { ErrorText } from "../components/ui.tsx";
import { useFetch } from "../lib/use-fetch.ts";
import { DeleteVolume, VolumeCard } from "./volumes.tsx";

/** One volume: its folder, usage and grants, and its backups. */
export function VolumeDetailsPage() {
  const { params } = useRoute();
  const { route } = useLocation();
  const name = params.name ?? "";
  const list = useFetch(() => api.volumes(), "volumes");
  const envs = useFetch(() => api.environments(), "environments");
  const [removing, setRemoving] = useState(false);
  const v = list.data?.volumes.find((x) => x.name === name);
  return (
    <Page>
      <div class="mb-4 text-sm">
        <a href="/volumes">Storage</a> / {name}
      </div>
      <ErrorText error={list.error} />
      {list.data && !v && <p class="text-muted">There's no volume {name}.</p>}
      {v && (
        <>
          <VolumeCard volume={v} environments={envs.data?.environments} onDelete={() => setRemoving(true)} linked={false} />
          <h2 class="mt-8 mb-3 text-xl font-bold">Backups</h2>
          <BackupsPanel kind="volume" name={v.name} stoppers={v.environments.filter((g) => envs.data?.environments.find((e) => e.name === g.name)?.state === "running").map((g) => g.name)} />
          {removing && <DeleteVolume volume={v} onClose={() => setRemoving(false)} onDeleted={() => route("/volumes")} />}
        </>
      )}
    </Page>
  );
}
