import { useEffect, useState } from "preact/hooks";
import { LocationProvider, Route, Router, useLocation } from "preact-iso";
import { api, type Me } from "./api.ts";
import { Toaster } from "./components/toast.tsx";
import { LiveProvider } from "./live.tsx";
import { AccountsPage } from "./pages/accounts.tsx";
import { CliLoginPage } from "./pages/cli-login.tsx";
import { DetailsPage } from "./pages/details.tsx";
import { DevicesPage } from "./pages/devices.tsx";
import { EnvironmentsPage } from "./pages/environments.tsx";
import { EnvironmentPage } from "./desktop/desktop.tsx";
import { VPNPage } from "./pages/vpn.tsx";
import { ImagesPage } from "./pages/images.tsx";
import { StoragePage } from "./pages/volumes.tsx";
import { VolumeDetailsPage } from "./pages/volume-details.tsx";
import { LinkPage } from "./pages/link.tsx";
import { LoginPage } from "./pages/login.tsx";
import { NotFound } from "./pages/not-found.tsx";

/**
 * `/<name>/` (one segment with a trailing slash) is an environment's page, with
 * its desktop and terminal tabs; the UI's own pages have no trailing slash.
 */
export function desktopName(path: string): string | null {
  const m = /^\/([a-z][a-z0-9-]*)\/$/.exec(path);
  return m ? (m[1] ?? null) : null;
}

export function App() {
  return (
    <LocationProvider>
      <Shell />
      <Toaster />
    </LocationProvider>
  );
}

/**
 * /login and /link (a login link) work signed out; /cli-login asks for a
 * session itself, through the API, so it can come back with its fragment.
 */
function Shell() {
  const { path } = useLocation();
  if (path === "/login") return <LoginPage />;
  if (path === "/link") return <LinkPage />;
  if (path === "/cli-login") return <CliLoginPage />;
  return <SignedIn />;
}

function SignedIn() {
  // preact-iso's `path` drops the trailing slash that marks a desktop page; `url` keeps it.
  const { url } = useLocation();
  const [me, setMe] = useState<Me | null>(null);
  useEffect(() => {
    // A 401 sends the browser to /login.
    api.me().then(setMe, () => undefined);
  }, []);
  if (!me) return null;

  const desktop = desktopName(url.split("?")[0] ?? "");
  return (
    <LiveProvider me={me}>
      {desktop ? (
        <EnvironmentPage key={desktop} name={desktop} />
      ) : (
        <Router>
          <Route path="/" component={EnvironmentsPage} />
          <Route path="/environments/:name" component={DetailsPage} />
          <Route path="/account" component={DevicesPage} />
          <Route path="/_vpn" component={me.admin ? VPNPage : NotFound} />
          <Route path="/images" component={me.admin ? ImagesPage : NotFound} />
          <Route path="/volumes" component={me.admin ? StoragePage : NotFound} />
          <Route path="/volumes/:name" component={me.admin ? VolumeDetailsPage : NotFound} />
          <Route path="/accounts" component={me.admin ? AccountsPage : NotFound} />
          <Route default component={NotFound} />
        </Router>
      )}
    </LiveProvider>
  );
}
