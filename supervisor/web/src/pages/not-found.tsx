import { Page, PageHead } from "../components/topbar.tsx";

export function NotFound() {
  return (
    <Page>
      <PageHead title="Not found" />
      <p class="text-muted">
        There's nothing here. <a href="/">Back to the computers</a>
      </p>
    </Page>
  );
}
