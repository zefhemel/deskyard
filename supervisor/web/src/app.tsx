import { useEffect, useState } from "preact/hooks";
import { LocationProvider, Route, Router, useLocation } from "preact-iso";
import { api, type Me } from "./api.ts";
import { Toaster } from "./components/toast.tsx";
import { LiveProvider } from "./live.tsx";
import { AccountsPage } from "./pages/accounts.tsx";
import { ComputerPage } from "./pages/computer.tsx";
import { ComputersPage } from "./pages/computers.tsx";
import { DesktopPage } from "./desktop/desktop.tsx";
import { ImagesPage } from "./pages/images.tsx";
import { LoginPage } from "./pages/login.tsx";
import { NotFound } from "./pages/not-found.tsx";

/**
 * `/<name>/` (one segment with a trailing slash) is a computer's desktop page;
 * the UI's own pages have no trailing slash.
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

function Shell() {
  const { path } = useLocation();
  if (path === "/login") return <LoginPage />;
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
        <DesktopPage key={desktop} name={desktop} />
      ) : (
        <Router>
          <Route path="/" component={ComputersPage} />
          <Route path="/computers/:name" component={ComputerPage} />
          <Route path="/images" component={me.admin ? ImagesPage : NotFound} />
          <Route path="/accounts" component={me.admin ? AccountsPage : NotFound} />
          <Route default component={NotFound} />
        </Router>
      )}
    </LiveProvider>
  );
}
