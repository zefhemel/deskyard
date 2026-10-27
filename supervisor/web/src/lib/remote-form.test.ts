import { test } from "node:test";
import assert from "node:assert/strict";
import { blankDraft, draftFromRemote, fieldsFor, remoteDraftErrors, toRemoteInput } from "./remote-form.ts";

test("each type asks for its own fields, with secrets marked", () => {
  assert.deepEqual(
    fieldsFor("sftp").map((f) => f.key),
    ["host", "port", "user", "password", "privateKey", "hostKey", "root"],
  );
  assert.ok(fieldsFor("s3").find((f) => f.key === "secretAccessKey")?.secret);
  assert.ok(fieldsFor("b2").find((f) => f.key === "applicationKey")?.secret);
  assert.equal(fieldsFor("webdav").find((f) => f.key === "url")?.required, true);
});

test("a new remote needs its type's required fields and secrets", () => {
  const sftp = { ...blankDraft("sftp"), name: "nas", host: "nas.local", user: "me" };
  assert.match(remoteDraftErrors(sftp, true).password ?? "", /password or a private key/);
  assert.deepEqual(remoteDraftErrors({ ...sftp, password: "pw" }, true), {});
  assert.deepEqual(remoteDraftErrors({ ...sftp, privateKey: "-----BEGIN" }, true), {});
  assert.match(remoteDraftErrors({ ...sftp, password: "pw", port: "99999" }, true).port ?? "", /1 to 65535/);
  assert.match(remoteDraftErrors({ ...sftp, password: "pw", name: "NAS" }, true).name ?? "", /lowercase/);
  const s3 = { ...blankDraft("s3"), name: "bucket", provider: "Minio", accessKeyId: "a", secretAccessKey: "s", root: "b/x" };
  assert.match(remoteDraftErrors(s3, true).endpoint ?? "", /endpoint/);
  assert.deepEqual(remoteDraftErrors({ ...s3, endpoint: "http://minio:9000" }, true), {});
  assert.match(remoteDraftErrors({ ...s3, provider: "AWS", root: "" }, true).root ?? "", /bucket/);
  assert.match(remoteDraftErrors({ ...s3, provider: "AWS", root: "/b" }, true).root ?? "", /bucket/);
  const dav = { ...blankDraft("webdav"), name: "cloud", url: "ftp://x" };
  assert.match(remoteDraftErrors(dav, true).url ?? "", /http/);
});

test("an edit keeps secrets left blank", () => {
  const draft = draftFromRemote({ name: "nas", type: "sftp", created: "", host: "nas.local", user: "me", root: "/b", secretsSet: ["password"], repositories: [] });
  assert.equal(draft.password, "");
  assert.deepEqual(remoteDraftErrors(draft, false, ["password"]), {});
  const { remote, secrets } = toRemoteInput(draft);
  assert.deepEqual(secrets, {});
  assert.deepEqual(remote, { name: "nas", type: "sftp", host: "nas.local", user: "me", root: "/b" });
});

test("the request carries only the type's fields and the secrets typed", () => {
  const d = { ...blankDraft("b2"), name: "bee", keyId: "k", applicationKey: "secret", root: "bucket", host: "stray" };
  assert.deepEqual(toRemoteInput(d), { remote: { name: "bee", type: "b2", keyId: "k", root: "bucket" }, secrets: { applicationKey: "secret" } });
  const s = { ...blankDraft("sftp"), name: "nas", host: "h", user: "u", port: "2222", password: "pw" };
  assert.deepEqual(toRemoteInput(s).remote, { name: "nas", type: "sftp", host: "h", port: 2222, user: "u" });
});
