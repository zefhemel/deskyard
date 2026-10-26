import type { ExceptionRow } from "../lib/vpn-form.ts";
import type { SavedVPNConnection, VPNStatus as VPNState } from "../api.ts";
import { vpnPresentation } from "../lib/vpn-form.ts";
import { Button, Field } from "./ui.tsx";

export function VPNStatus({ vpn, compact = false }: { vpn: VPNState | undefined; compact?: boolean }) {
  if (!vpn) return null;
  const view = vpnPresentation(vpn);
  return (
    <div class="text-sm" aria-label="VPN status">
      <span class={vpn.state === "blocked" ? "font-semibold text-danger" : vpn.state === "connected" ? "font-semibold text-run" : "text-muted"}>
        {view.label}
      </span>
      {vpn.name && (
        <span>
          {" "}
          · {vpn.name}
          {vpn.country ? ` · ${vpn.country}` : ""}
        </span>
      )}
      {!compact && (
        <>
          {view.currentIP && <p class="mt-1 text-muted">Current exit IP {view.currentIP}</p>}
          {view.history && <p class="mt-1 text-muted">{view.history}</p>}
          {view.detail && <p class="mt-1 text-muted">{view.detail}</p>}
          {view.notice && <p class="mt-1 text-muted">{view.notice}</p>}
        </>
      )}
    </div>
  );
}

export function VPNFields({
  connections,
  value,
  onChange,
  rows,
  onRows,
}: {
  connections: SavedVPNConnection[];
  value: string;
  onChange: (id: string) => void;
  rows: ExceptionRow[];
  onRows: (rows: ExceptionRow[]) => void;
}) {
  const selected = connections.find((c) => c.id === value);
  const update = (index: number, patch: Partial<ExceptionRow>) => onRows(rows.map((r, i) => (i === index ? { ...r, ...patch } : r)));
  return (
    <>
      <Field
        label="VPN connection"
        htmlFor="environment-vpn"
        hint={
          selected
            ? `NordVPN · ${selected.country}. Internet access stays blocked if the VPN is unavailable.`
            : value
              ? "The saved selection is unavailable. Refresh or choose another connection."
              : "No VPN uses direct internet access."
        }
      >
        <select id="environment-vpn" value={value} onChange={(e) => onChange(e.currentTarget.value)}>
          <option value="">No VPN</option>
          {value && !selected && <option value={value}>Selected connection unavailable</option>}
          {connections.map((c) => (
            <option key={c.id} value={c.id}>
              {c.name} · {c.country}
            </option>
          ))}
        </select>
        <p class="mt-1.5 text-[13px] text-muted">
          <a href="/_vpn">Manage VPN connections</a>
        </p>
      </Field>
      {value && (
        <Field
          label="Local network exceptions"
          hint="Allow only the listed private IPv4 destinations and ports directly outside the VPN. Other environment and reserved networks remain blocked. Agent port forwarding is unchanged."
        >
          {rows.map((row, i) => (
            <div key={i} class="mb-2 rounded-lg border border-line bg-white p-3">
              <div class="mb-2 flex gap-2">
                <input
                  type="text"
                  aria-label={`LAN destination ${i + 1}`}
                  placeholder="192.168.1.20 or 192.168.1.0/24"
                  value={row.destination}
                  onInput={(e) => update(i, { destination: e.currentTarget.value })}
                />
                <select
                  aria-label={`LAN transport ${i + 1}`}
                  class="w-24!"
                  value={row.protocol}
                  onChange={(e) => update(i, { protocol: e.currentTarget.value })}
                >
                  <option value="tcp">TCP</option>
                  <option value="udp">UDP</option>
                </select>
              </div>
              <div class="flex gap-2">
                <input
                  type="text"
                  aria-label={`LAN ports ${i + 1}`}
                  placeholder="Ports: 443, 8000-8002"
                  value={row.ports}
                  onInput={(e) => update(i, { ports: e.currentTarget.value })}
                />
                <Button onClick={() => onRows(rows.filter((_, n) => n !== i))}>Remove</Button>
              </div>
            </div>
          ))}
          <Button onClick={() => onRows([...rows, { destination: "", protocol: "tcp", ports: "" }])}>Add LAN exception</Button>
        </Field>
      )}
    </>
  );
}
