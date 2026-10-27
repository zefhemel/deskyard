import type { Remote, RemoteInput, RemoteSecrets, RemoteType } from "../api.ts";

/** The remote form's state: every field as typed, secrets blank unless typed. */
export interface RemoteDraft {
  name: string;
  type: RemoteType;
  root: string;
  provider: string;
  endpoint: string;
  region: string;
  accessKeyId: string;
  keyId: string;
  host: string;
  port: string;
  user: string;
  hostKey: string;
  url: string;
  vendor: string;
  secretAccessKey: string;
  applicationKey: string;
  password: string;
  privateKey: string;
}
type Key = Exclude<keyof RemoteDraft, "name" | "type">;
type SecretKey = "secretAccessKey" | "applicationKey" | "password" | "privateKey";
const SECRETS: SecretKey[] = ["secretAccessKey", "applicationKey", "password", "privateKey"];

export interface FieldSpec {
  key: Key;
  label: string;
  secret?: boolean;
  required?: boolean;
  multiline?: boolean;
  options?: string[];
  placeholder?: string;
  hint?: string;
}

export const REMOTE_TYPES: { type: RemoteType; label: string; hint: string }[] = [
  { type: "s3", label: "S3-compatible", hint: "AWS S3, Cloudflare R2, MinIO, Wasabi and other S3 services" },
  { type: "b2", label: "Backblaze B2", hint: "A B2 bucket, with an application key" },
  { type: "sftp", label: "SFTP", hint: "A NAS or server you can reach over SSH" },
  { type: "webdav", label: "WebDAV", hint: "Nextcloud, ownCloud or another WebDAV server" },
];

export const S3_PROVIDERS = ["AWS", "Cloudflare", "Minio", "Wasabi", "Other"];
export const WEBDAV_VENDORS = ["nextcloud", "owncloud", "other"];

const FIELDS: Record<RemoteType, FieldSpec[]> = {
  s3: [
    { key: "provider", label: "Provider", required: true, options: S3_PROVIDERS },
    { key: "endpoint", label: "Endpoint", placeholder: "https://…", hint: "Empty for AWS." },
    { key: "region", label: "Region", placeholder: "us-east-1" },
    { key: "accessKeyId", label: "Access key ID", required: true },
    { key: "secretAccessKey", label: "Secret access key", secret: true, required: true },
    { key: "root", label: "Bucket and folder", required: true, placeholder: "my-bucket/deskyard" },
  ],
  b2: [
    { key: "keyId", label: "Key ID", required: true },
    { key: "applicationKey", label: "Application key", secret: true, required: true },
    { key: "root", label: "Bucket and folder", required: true, placeholder: "my-bucket" },
  ],
  sftp: [
    { key: "host", label: "Host", required: true, placeholder: "nas.local" },
    { key: "port", label: "Port", placeholder: "22" },
    { key: "user", label: "User", required: true },
    { key: "password", label: "Password", secret: true, hint: "A password or a private key." },
    { key: "privateKey", label: "Private key", secret: true, multiline: true, placeholder: "-----BEGIN OPENSSH PRIVATE KEY-----" },
    { key: "hostKey", label: "Host key", placeholder: "nas.local ssh-ed25519 AAAA…", hint: "Optional known_hosts line that pins the server's key." },
    { key: "root", label: "Folder", placeholder: "/volume1/backups", hint: "Empty is the user's home." },
  ],
  webdav: [
    { key: "url", label: "URL", required: true, placeholder: "https://cloud.example.com/remote.php/dav/files/me" },
    { key: "vendor", label: "Server", options: WEBDAV_VENDORS },
    { key: "user", label: "User" },
    { key: "password", label: "Password", secret: true },
    { key: "root", label: "Folder", placeholder: "Backups" },
  ],
};

/** The fields of a type, in form order. */
export function fieldsFor(type: RemoteType): FieldSpec[] {
  return FIELDS[type];
}

export function blankDraft(type: RemoteType): RemoteDraft {
  return {
    name: "", type, root: "", provider: type === "s3" ? "AWS" : "", endpoint: "", region: "", accessKeyId: "", keyId: "", host: "", port: "", user: "",
    hostKey: "", url: "", vendor: type === "webdav" ? "other" : "", secretAccessKey: "", applicationKey: "", password: "", privateKey: "",
  };
}

/** A saved remote in the form, secrets blank. */
export function draftFromRemote(r: Remote): RemoteDraft {
  const d = blankDraft(r.type);
  for (const k of Object.keys(d) as (keyof RemoteDraft)[]) {
    const v = (r as unknown as Record<string, unknown>)[k];
    if (typeof v === "string") (d as unknown as Record<string, string>)[k] = v;
    if (k === "port" && typeof v === "number") d.port = String(v);
  }
  if (r.type === "webdav" && !r.vendor) d.vendor = "other";
  return d;
}

const NAME = /^[a-z][a-z0-9-]{0,30}[a-z0-9]$/;
const isURL = (s: string) => /^https?:\/\/[^/\s]+/.test(s);

/** Why the draft can't be saved, by field; empty when it can. `secretsSet` names the stored secrets an edit keeps. */
export function remoteDraftErrors(d: RemoteDraft, creating: boolean, secretsSet: string[] = []): Record<string, string> {
  const errs: Record<string, string> = {};
  const has = (k: SecretKey) => d[k] !== "" || (!creating && secretsSet.includes(k));
  if (!NAME.test(d.name)) errs.name = "Use 2–32 lowercase letters, digits and dashes, starting with a letter.";
  for (const f of fieldsFor(d.type)) {
    const missing = f.secret ? !has(f.key as SecretKey) : !d[f.key].trim();
    if (f.required && missing) errs[f.key] = `${f.label} is needed.`;
  }
  for (const f of fieldsFor(d.type)) if (!f.multiline && /[\r\n]/.test(d[f.key])) errs[f.key] = "One line, please.";
  if (d.root.split("/").includes("..")) errs.root = "No .. in the path.";
  switch (d.type) {
    case "s3":
      if (d.provider !== "AWS" && !d.endpoint) errs.endpoint = "An endpoint is needed for providers other than AWS.";
      if (d.endpoint && !isURL(d.endpoint)) errs.endpoint = "An http or https URL.";
      if (!d.root || d.root.startsWith("/")) errs.root = "Start with the bucket, like my-bucket/backups.";
      break;
    case "b2":
      if (!d.root || d.root.startsWith("/")) errs.root = "Start with the bucket, like my-bucket/backups.";
      break;
    case "sftp": {
      if (!has("password") && !has("privateKey")) errs.password = "Enter a password or a private key.";
      const port = Number(d.port);
      if (d.port && (!Number.isInteger(port) || port < 1 || port > 65535)) errs.port = "A port is 1 to 65535.";
      break;
    }
    case "webdav":
      if (!isURL(d.url)) errs.url = "An http or https URL.";
      break;
  }
  return errs;
}

/** The request for the draft: only its type's fields, and only the secrets typed. */
export function toRemoteInput(d: RemoteDraft): { remote: RemoteInput; secrets: RemoteSecrets } {
  const remote: Record<string, string | number> = { name: d.name, type: d.type };
  const secrets: RemoteSecrets = {};
  for (const f of fieldsFor(d.type)) {
    const v = f.multiline ? d[f.key] : d[f.key].trim();
    if (!v) continue;
    if (f.secret) secrets[f.key as SecretKey] = v;
    else if (f.key === "port") remote.port = Number(v);
    else remote[f.key] = v;
  }
  for (const k of SECRETS) if (!(k in secrets)) delete (secrets as Record<string, string>)[k];
  return { remote: remote as unknown as RemoteInput, secrets };
}
