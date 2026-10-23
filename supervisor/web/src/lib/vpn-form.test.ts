import { test } from "node:test";
import assert from "node:assert/strict";
import { connectionInput, desktopVPNPatch, parseExceptions, vpnPresentation, connectionUpdate } from "./vpn-form.ts";

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
test("desktop patch omits unchanged settings and sends explicit null to disable VPN", () => {
  const original = { vpnConnectionId: "v1", lanExceptions: [] };
  assert.deepEqual(desktopVPNPatch(original, "v1", []), {});
  assert.deepEqual(desktopVPNPatch(original, "", []), { vpnConnectionId: null });
  assert.deepEqual(desktopVPNPatch({ vpnConnectionId: null, lanExceptions: [] }, "v1", []), { vpnConnectionId: "v1" });
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
  assert.deepEqual(desktopVPNPatch(original, "v1", []), { lanExceptions: [] });
  assert.deepEqual(desktopVPNPatch(original, "", original.lanExceptions), { vpnConnectionId: null });
});
const status = {
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
test("shared edit submits the exact preview revision and stable desktop IDs", () => {
  assert.deepEqual(
    connectionUpdate(form, {
      revision: 7,
      desktops: [
        { id: "stable-2", name: "alice" },
        { id: "stable-1", name: "bob" },
      ],
    }),
    { name: "Travel", provider: "nordvpn", country: "Poland", expectedRevision: 7, affectedDesktopIds: ["stable-2", "stable-1"], confirmRestart: true },
  );
});
