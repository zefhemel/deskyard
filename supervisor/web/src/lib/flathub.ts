// Flathub's public search, called from the admin's browser.
export interface FlathubApp {
  id: string;
  name: string;
  summary: string;
}

/** Throws when Flathub can't be reached; callers fall back to a plain ID field. */
export async function searchFlathub(query: string, signal?: AbortSignal): Promise<FlathubApp[]> {
  const res = await fetch("https://flathub.org/api/v2/search", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ query, hits_per_page: 8 }),
    signal: signal ?? null,
  });
  if (!res.ok) throw new Error(`Flathub answered ${res.status}`);
  const data = (await res.json()) as { hits?: { app_id?: string; name?: string; summary?: string }[] };
  return (data.hits ?? []).flatMap((h) => (h.app_id ? [{ id: h.app_id, name: h.name ?? h.app_id, summary: h.summary ?? "" }] : []));
}
