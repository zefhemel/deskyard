import { test } from "node:test";
import assert from "node:assert/strict";
import {
  connectionInput,
  environmentVPNPatch,
  parseExceptions,
  vpnPresentation,
  connectionUpdate,
  CandidateTestSession,
  environmentSettingsChanged,
  saveEnvironmentSettings,
} from "./vpn-form.ts";

const form = { name: "Travel", country: "Poland", username: "", password: "" };
test("blank edit credentials are independently omitted, replacement preserves the other secret", () => {
  assert.deepEqual(connectionInput(form, true), { name: "Travel", provider: "nordvpn", country: "Poland" });
  assert.deepEqual(connectionInput({ ...form, password: "new-secret" }, true), {
    name: "Travel",
    provider: "nordvpn",
    country: "Poland",
    password: "new-secret",
  });
  assert.throws(() => connectionInput(form, false), /service credentials/i);
});
test("environment patch omits unchanged settings and sends explicit null to disable VPN", () => {
  const original = { vpnConnectionId: "v1", lanExceptions: [] };
  assert.deepEqual(environmentVPNPatch(original, "v1", []), {});
  assert.deepEqual(environmentVPNPatch(original, "", []), { vpnConnectionId: null });
  assert.deepEqual(environmentVPNPatch({ vpnConnectionId: null, lanExceptions: [] }, "v1", []), { vpnConnectionId: "v1" });
});
test("LAN rows retain explicit transport and inclusive port limits and normalize private addresses", () => {
  assert.deepEqual(parseExceptions([{ destination: "192.168.2.34/24", protocol: "tcp", ports: "443, 8000-8002" }]), [
    {
      destination: "192.168.2.0/24",
      protocol: "tcp",
      ports: [
        { start: 443, end: 443 },
        { start: 8000, end: 8002 },
      ],
    },
  ]);
});
test("invalid LAN addresses, protocols and port ranges never become payloads", () => {
  for (const destination of ["0.0.0.0/0", "8.8.8.8", "169.254.169.254", "::1", "192.168.1.999", "192.168.1.1/8"])
    assert.throws(() => parseExceptions([{ destination, protocol: "tcp", ports: "443" }]));
  for (const ports of ["", "0", "65536", "80-1", "1-65535", "80junk", "1-2-3"])
    assert.throws(() => parseExceptions([{ destination: "10.1.1.1", protocol: "tcp", ports }]));
  assert.throws(() => parseExceptions([{ destination: "10.1.1.1", protocol: "icmp", ports: "443" }]));
});
test("clearing exceptions sends empty rules but unrelated edits retain them", () => {
  const original = { vpnConnectionId: "v1", lanExceptions: [{ destination: "10.1.1.1", protocol: "tcp" as const, ports: [{ start: 443, end: 443 }] }] };
  assert.deepEqual(environmentVPNPatch(original, "v1", []), { lanExceptions: [] });
  assert.deepEqual(environmentVPNPatch(original, "", original.lanExceptions), { vpnConnectionId: null });
});
const status = {
  connectionId: "v1",
  state: "blocked" as const,
  changedAt: "2026-10-23T01:00:00Z",
  desiredRevision: 2,
  appliedRevision: 1,
  idle: false,
  currentExitIp: "203.0.113.1",
  lastExitIp: "203.0.113.2",
  exitIpObservedAt: "2026-10-23T00:00:00Z",
};
test("only connected status exposes current exit IP; historical IP has observation context", () => {
  assert.equal(vpnPresentation(status).currentIP, undefined);
  assert.match(vpnPresentation(status).history ?? "", /203.0.113.2.*2026/);
  assert.equal(vpnPresentation({ ...status, state: "connected" }).currentIP, "203.0.113.1");
  assert.equal(vpnPresentation({ ...status, exitIpObservedAt: "0001-01-01T00:00:00Z" }).history, undefined);
});
test("idle status explains the provider connection and pending revision remains visible", () => {
  assert.equal(vpnPresentation({ ...status, state: "stopped", idle: true }).label, "VPN idle");
  assert.match(vpnPresentation({ ...status, state: "stopped", idle: true }).detail, /provider connection/);
  assert.match(vpnPresentation(status).detail, /2.*1/);
});
test("shared edit submits the exact preview revision and the affected environments by name", () => {
  assert.deepEqual(
    connectionUpdate(form, {
      revision: 7,
      environments: [
        { id: "alice", name: "alice" },
        { id: "bob", name: "bob" },
      ],
    }),
    { name: "Travel", provider: "nordvpn", country: "Poland", expectedRevision: 7, affectedEnvironments: ["alice", "bob"], confirmRestart: true },
  );
});

test("invalid retest keeps the prior candidate success hidden and makes no second request", async () => {
  const session = new CandidateTestSession();
  let requests = 0;
  const completed = { status: "done" as const, message: "Done", progress: 1, result: { ...status, state: "connected" as const } };
  const begin = async () => {
    requests++;
    return { job: "candidate-a" };
  };
  await session.start(form, true, begin);
  assert.deepEqual(session.result(form, completed), completed);
  for (const invalid of [
    { ...form, name: "" },
    { ...form, country: "" },
  ]) {
    assert.equal(session.result(invalid, completed), null);
    let localError = "";
    try {
      await session.start(invalid, true, begin);
    } catch (e) {
      localError = (e as Error).message;
    }
    assert.match(localError, /name|country/);
    assert.equal(requests, 1);
    assert.equal(session.result(invalid, completed), null);
  }
});

test("accepted canonical LAN settings clear dirty state and repeated saves make no request or job", async () => {
  let saved = {
    image: "img",
    settings: { dockerAccess: false, mounts: [] as import("../api.ts").HostMount[], volumes: [], memoryBytes: 0, nanoCpus: 0, camera: "", vpnConnectionId: "v1", lanExceptions: [] as import("../api.ts").LANException[] },
  };
  const draft = {
    image: "img",
    memoryBytes: 0,
    nanoCpus: 0,
    camera: "",
    vpnConnectionId: "v1",
    lan: [{ destination: "192.168.1.20", protocol: "tcp", ports: "8000-8002,443" }],
  };
  let requests = 0;
  const begin = async (body: import("../api.ts").SettingsPatch) => {
    requests++;
    assert.equal(body.vpnConnectionId, undefined);
    saved = {
      image: "img",
      settings: {
        dockerAccess: false,
        mounts: [] as import("../api.ts").HostMount[], volumes: [],
        memoryBytes: 0,
        nanoCpus: 0,
        camera: "",
        vpnConnectionId: "v1",
        lanExceptions: [
          {
            destination: "192.168.1.20/32",
            protocol: "tcp",
            ports: [
              { start: 443, end: 443 },
              { start: 8000, end: 8002 },
            ],
          },
        ],
      },
    };
    return { job: "settings-1" };
  };
  assert.equal(environmentSettingsChanged(saved, draft), true);
  assert.deepEqual(await saveEnvironmentSettings(saved, draft, begin), { job: "settings-1" });
  assert.equal(environmentSettingsChanged(saved, draft), false);
  assert.equal(await saveEnvironmentSettings(saved, draft, begin), null);
  assert.equal(requests, 1);
  const reformatted = { ...draft, lan: [{ destination: "192.168.1.20/32", protocol: "tcp", ports: "443, 8001-8002,8000,443" }] };
  assert.equal(environmentSettingsChanged(saved, reformatted), false);
  assert.equal(await saveEnvironmentSettings(saved, reformatted, begin), null);
  assert.equal(requests, 1);
});

test("No VPN ignores hidden LAN drafts without discarding them and omits saved rules", async () => {
  const saved = {
    image: "img",
    settings: {
      dockerAccess: false,
      mounts: [],
      volumes: [],
      memoryBytes: 0,
      nanoCpus: 0,
      camera: "",
      vpnConnectionId: "v1",
      lanExceptions: [{ destination: "192.168.1.20/32", protocol: "tcp" as const, ports: [{ start: 443, end: 443 }] }],
    },
  };
  const hiddenRows = [{ destination: "unfinished", protocol: "tcp", ports: "" }];
  const draft = { image: "img", memoryBytes: 0, nanoCpus: 0, camera: "", vpnConnectionId: "", lan: hiddenRows };
  let requests = 0;
  await saveEnvironmentSettings(saved, draft, async (body) => {
    requests++;
    assert.deepEqual(body, { vpnConnectionId: null });
    return { job: "disable" };
  });
  assert.equal(requests, 1);
  assert.deepEqual(draft.lan, [{ destination: "unfinished", protocol: "tcp", ports: "" }]);
});

test("pending explicit No VPN describes the network transition without asking for a VPN connection", () => {
  const view = vpnPresentation({ ...status, connectionId: "", desiredRevision: 0, reason: "Network transition failed at consumer-create; retry to recover" });
  assert.match(view.label, /Network transition/);
  assert.doesNotMatch(view.detail, /selected connection/);
  assert.match(view.notice ?? "", /network transition/i);
  assert.doesNotMatch(view.notice ?? "", /until the VPN connects/);
  assert.match(vpnPresentation({ ...status, connectionId: "v1" }).notice ?? "", /until the VPN connects/);
});

 test("host folder-only edits and VPN edits share one settings payload", async () => {
 const saved = {image:"img",settings:{dockerAccess:false,memoryBytes:0,nanoCpus:0,camera:"",vpnConnectionId:"v1",lanExceptions:[],mounts:[],volumes:[]}};
 const mounts=[{source:"/srv/photos",target:"/mnt/photos",readOnly:true}];
 const draft={image:"img",memoryBytes:0,nanoCpus:0,camera:"",vpnConnectionId:"v1",lan:[],mounts};
 assert.equal(environmentSettingsChanged(saved,draft),true);
 const body=await saveEnvironmentSettings(saved,draft,async body=>body);
 assert.deepEqual(body,{mounts});
 assert.deepEqual(await saveEnvironmentSettings(saved,{...draft,vpnConnectionId:""},async body=>body),{vpnConnectionId:null,mounts});
 const updated={...saved,settings:{...saved.settings,mounts}};
 assert.equal(environmentSettingsChanged(updated,draft),false);
 assert.equal(await saveEnvironmentSettings(updated,draft,async body=>body),null);
 });

test("Docker permission-only changes save once and can be revoked", async () => {
  const saved = { image: "img", settings: { dockerAccess: false, memoryBytes: 0, nanoCpus: 0, camera: "", vpnConnectionId: null, lanExceptions: [], mounts: [], volumes: [] } };
  const draft = { image: "img", dockerAccess: true, memoryBytes: 0, nanoCpus: 0, camera: "", vpnConnectionId: "", lan: [] };
  assert.equal(environmentSettingsChanged(saved, draft), true);
  assert.deepEqual(await saveEnvironmentSettings(saved, draft, async (body) => body), { dockerAccess: true });
  const updated = { ...saved, settings: { ...saved.settings, dockerAccess: true } };
  assert.equal(environmentSettingsChanged(updated, draft), false);
  assert.equal(await saveEnvironmentSettings(updated, draft, async (body) => body), null);
  assert.deepEqual(await saveEnvironmentSettings(updated, { ...draft, dockerAccess: false }, async (body) => body), { dockerAccess: false });
});
