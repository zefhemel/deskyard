import assert from "node:assert/strict";
import { test } from "node:test";
import { davAddress, davInTheClear, davView } from "./webdav.ts";

const at = (url: string) => {
  const u = new URL(url);
  return { origin: u.origin, protocol: u.protocol, hostname: u.hostname };
};

test("the drive is at /<name>/dav/ on the page's origin", () => {
  assert.equal(davAddress("dev", at("https://desktops.example.com/dev/")), "https://desktops.example.com/dev/dav/");
  assert.equal(davAddress("dev", at("http://192.168.1.5:7343/")), "http://192.168.1.5:7343/dev/dav/");
});

test("only plain HTTP from another machine is in the clear", () => {
  assert.equal(davInTheClear(at("https://desktops.example.com/")), false);
  assert.equal(davInTheClear(at("http://192.168.1.5:7343/")), true);
  assert.equal(davInTheClear(at("http://deskyard.lan/")), true);
  for (const local of ["http://localhost:7343/", "http://127.0.0.1:7343/", "http://[::1]:7343/"]) {
    assert.equal(davInTheClear(at(local)), false, local);
  }
});

test("the Access tab shows admins the switch, and others with full access the address while it's on", () => {
  const admin = { admin: true, kind: "password", share: false } as const;
  const anna = { admin: false, kind: "password", share: false } as const;
  assert.equal(davView({ webdav: false, level: "full" }, admin), "switch");
  assert.equal(davView({ webdav: true, level: "full" }, admin), "switch");
  assert.equal(davView({ webdav: true, level: "full" }, anna), "address");
  assert.equal(davView({ webdav: false, level: "full" }, anna), null);
  assert.equal(davView({ webdav: true, level: "observe" }, anna), null);
  assert.equal(davView({ webdav: true, level: "full" }, { ...anna, kind: "token" }), null);
  assert.equal(davView({ webdav: true, level: "full" }, { ...anna, share: true }), null);
});
