import { useEffect, useState } from "preact/hooks";
import { api, type Computer, type ServiceRule } from "../api.ts";
import { useJob, JobProgress } from "./job.tsx";
import { Button, Field, ErrorText, Note } from "./ui.tsx";
import { notify } from "./toast.tsx";

export function TailscaleServices({ computer }: { computer: Computer }) {
  const saved = JSON.stringify(computer.settings.services ?? []);
  const [rules, setRules] = useState<ServiceRule[]>(computer.settings.services ?? []);
  useEffect(() => setRules(JSON.parse(saved) as ServiceRule[]), [saved]);
  const run = useJob(() => notify("HTTP service settings saved"), computer.name);
  const update = (i: number, patch: Partial<ServiceRule>) => setRules(rules.map((r, j) => i === j ? {...r, ...patch} : r));
  return <section class="mt-6 border-t border-line pt-5" aria-label="Tailscale HTTP services">
    <h3 class="mb-2 font-semibold">HTTP services</h3>
    <p class="mb-4 text-sm text-muted">Share an app listening on a local port. Private Serve is limited by your tailnet access rules. Public Funnel lets anyone reach the app; the app supplies its own login.</p>
    {rules.map((r, i) => <div class="mb-3 rounded-lg border border-line p-3" key={i}>
      <Field label="Label"><input type="text" aria-label={`Service label ${i+1}`} value={r.label ?? ""} onInput={e=>update(i,{label:e.currentTarget.value})}/></Field>
      <div class="grid grid-cols-2 gap-3">
        <Field label="Local HTTP port"><input aria-label={`Local HTTP port ${i+1}`} type="number" min="1" max="65535" value={r.localPort} onInput={e=>update(i,{localPort:Number(e.currentTarget.value)})}/></Field>
        <Field label="HTTPS listener port"><input aria-label={`HTTPS listener port ${i+1}`} type="number" min="1" max="65535" value={r.listenPort} onInput={e=>update(i,{listenPort:Number(e.currentTarget.value)})}/></Field>
      </div>
      <Field label="Audience"><select aria-label={`Service audience ${i+1}`} value={r.audience} onChange={e=>update(i,{audience:e.currentTarget.value as "private"|"public"})}><option value="private">Private · Serve</option><option value="public">Public internet · Funnel</option></select></Field>
      {r.audience === "public" && <p class="mb-3 text-sm text-muted">Funnel listener ports: 443, 8443 or 10000. Each listener can publish one app.</p>}
      <Button onClick={()=>setRules(rules.filter((_,j)=>i!==j))}>Remove service</Button>
    </div>)}
    <div class="mb-3 flex gap-2">
      <Button disabled={run.running} onClick={()=>setRules([...rules,{localPort:3000,listenPort:!rules.some(r=>r.listenPort===443)?443:!rules.some(r=>r.listenPort===8443)?8443:10000,audience:"private"}])}>Add HTTP service</Button>
      <Button variant="primary" disabled={run.running || JSON.stringify(rules)===saved} onClick={()=>void run.start(()=>api.updateNetworkServices(computer.name,rules))}>Save HTTP services</Button>
    </div>
    <Note>Service edits apply without restarting the desktop. For a stopped computer, settings apply on its next start. Listen on 127.0.0.1 inside the computer; each app is published at its listener’s root path.</Note>
    <JobProgress job={run.job}/><ErrorText error={run.error}/>
  </section>;
}
