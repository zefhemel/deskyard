import { test } from "node:test";
import assert from "node:assert/strict";
import { addGrant, canAddGrant, grantPathError, removeGrant, updateGrant } from "./volume-grants.ts";
import { environmentSettingsChanged, saveEnvironmentSettings } from "./vpn-form.ts";

const volumes = [{ name: "music" }, { name: "photos" }];

test("adding a volume grant defaults to read-write at /share/<volume> and picks a volume not yet granted", () => {
  assert.deepEqual(addGrant([], volumes), [{ name: "music", path: "/share/music", readOnly: false }]);
  assert.deepEqual(addGrant([{ name: "music", path: "/share/music", readOnly: false }], volumes), [
    { name: "music", path: "/share/music", readOnly: false },
    { name: "photos", path: "/share/photos", readOnly: false },
  ]);
});

test("a grant can be added while an ungranted volume remains, with no limit on their number", () => {
  assert.equal(canAddGrant([], []), false);
  assert.equal(canAddGrant([], volumes), true);
  assert.equal(canAddGrant([{ name: "music", path: "/share/music", readOnly: true }, { name: "photos", path: "/share/photos", readOnly: true }], volumes), false);
  const many = Array.from({ length: 40 }, (_, i) => ({ name: `v${i}`, path: `/share/v${i}`, readOnly: true }));
  assert.equal(canAddGrant(many, [...many, { name: "more" }]), true);
});

test("choosing another volume moves a grant still at its default path, and keeps a chosen one", () => {
  const grants = [{ name: "music", path: "/share/music", readOnly: false }];
  assert.deepEqual(updateGrant(grants, 0, { name: "photos" }), [{ name: "photos", path: "/share/photos", readOnly: false }]);
  const custom = [{ name: "music", path: "/mnt/m", readOnly: false }];
  assert.deepEqual(updateGrant(custom, 0, { name: "photos" }), [{ name: "photos", path: "/mnt/m", readOnly: false }]);
});

test("grant paths follow the supervisor's rules", () => {
  const ok = (path: string, others: { name: string; path: string; readOnly: boolean }[] = []) =>
    grantPathError(path, [{ name: "x", path, readOnly: false }, ...others], 0);
  for (const p of ["/share/a", "/mnt/photos", "/home/user/x", "/data", "/library"]) assert.equal(ok(p), null, p);
  for (const [p, want] of [
    ["", /absolute/],
    ["relative", /absolute/],
    ["/share/../etc", /clean/],
    ["/share/", /clean/],
    ["/", /whole/],
    ["/workspace", /home/],
    ["/workspace/x", /home/],
    ["/usr/local", /\/usr/],
    ["/lib64", /\/lib64/],
    ["/var/lib", /\/var/],
    ["/opt/x", /\/opt/],
  ] as const) {
    assert.match(ok(p) ?? "", want, p);
  }
  assert.match(ok("/data", [{ name: "y", path: "/data/sub", readOnly: false }]) ?? "", /overlap/);
  assert.match(ok("/data/sub", [{ name: "y", path: "/data", readOnly: false }]) ?? "", /overlap/);
  assert.equal(ok("/ab", [{ name: "y", path: "/a", readOnly: false }]), null);
});

test("removing a volume grant drops it and keeps the others", () => {
  const grants = [
    { name: "music", path: "/share/music", readOnly: true },
    { name: "photos", path: "/share/photos", readOnly: false },
  ];
  assert.deepEqual(removeGrant(grants, 0), [{ name: "photos", path: "/share/photos", readOnly: false }]);
  assert.deepEqual(grants.length, 2);
});

test("updating a grant changes only that grant", () => {
  const grants = [
    { name: "music", path: "/share/music", readOnly: true },
    { name: "photos", path: "/share/photos", readOnly: true },
  ];
  assert.deepEqual(updateGrant(grants, 1, { readOnly: false }), [
    { name: "music", path: "/share/music", readOnly: true },
    { name: "photos", path: "/share/photos", readOnly: false },
  ]);
});

test("volume grant edits are a settings change sent as the complete list", async () => {
  const saved = { image: "img", settings: { dockerAccess: false, memoryBytes: 0, nanoCpus: 0, vpnConnectionId: null, lanExceptions: [], volumes: [] } };
  const grants = [{ name: "photos", path: "/share/photos", readOnly: true }];
  const draft = { image: "img", memoryBytes: 0, nanoCpus: 0, vpnConnectionId: "", lan: [], volumes: grants };
  assert.equal(environmentSettingsChanged(saved, draft), true);
  assert.deepEqual(await saveEnvironmentSettings(saved, draft, async (body) => body), { volumes: grants });
  const updated = { ...saved, settings: { ...saved.settings, volumes: grants } };
  assert.equal(environmentSettingsChanged(updated, draft), false);
  assert.deepEqual(await saveEnvironmentSettings(updated, { ...draft, volumes: [] }, async (body) => body), { volumes: [] });
});
