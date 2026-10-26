import { test } from "node:test";
import assert from "node:assert/strict";
import { addGrant, canAddGrant, MAX_GRANTS, removeGrant, updateGrant } from "./volume-grants.ts";
import { environmentSettingsChanged, saveEnvironmentSettings } from "./vpn-form.ts";

const volumes = [{ name: "music" }, { name: "photos" }];

test("adding a volume grant defaults to read-write and picks a volume not yet granted", () => {
  assert.deepEqual(addGrant([], volumes), [{ name: "music", readOnly: false }]);
  assert.deepEqual(addGrant([{ name: "music", readOnly: false }], volumes), [
    { name: "music", readOnly: false },
    { name: "photos", readOnly: false },
  ]);
});

test("a grant can be added while an ungranted volume remains and the limit isn't reached", () => {
  assert.equal(canAddGrant([], []), false);
  assert.equal(canAddGrant([], volumes), true);
  assert.equal(canAddGrant([{ name: "music", readOnly: true }, { name: "photos", readOnly: true }], volumes), false);
  const many = Array.from({ length: MAX_GRANTS }, (_, i) => ({ name: `v${i}`, readOnly: true }));
  assert.equal(canAddGrant(many, [...volumes, ...many]), false);
});

test("removing a volume grant drops it and keeps the others", () => {
  const grants = [
    { name: "music", readOnly: true },
    { name: "photos", readOnly: false },
  ];
  assert.deepEqual(removeGrant(grants, 0), [{ name: "photos", readOnly: false }]);
  assert.deepEqual(grants.length, 2);
});

test("updating a grant changes only that grant", () => {
  const grants = [
    { name: "music", readOnly: true },
    { name: "photos", readOnly: true },
  ];
  assert.deepEqual(updateGrant(grants, 1, { readOnly: false }), [
    { name: "music", readOnly: true },
    { name: "photos", readOnly: false },
  ]);
});

test("volume grant edits are a settings change sent as the complete list", async () => {
  const saved = { image: "img", settings: { dockerAccess: false, memoryBytes: 0, nanoCpus: 0, vpnConnectionId: null, lanExceptions: [], mounts: [], volumes: [] } };
  const grants = [{ name: "photos", readOnly: true }];
  const draft = { image: "img", memoryBytes: 0, nanoCpus: 0, vpnConnectionId: "", lan: [], volumes: grants };
  assert.equal(environmentSettingsChanged(saved, draft), true);
  assert.deepEqual(await saveEnvironmentSettings(saved, draft, async (body) => body), { volumes: grants });
  const updated = { ...saved, settings: { ...saved.settings, volumes: grants } };
  assert.equal(environmentSettingsChanged(updated, draft), false);
  assert.deepEqual(await saveEnvironmentSettings(updated, { ...draft, volumes: [] }, async (body) => body), { volumes: [] });
});
