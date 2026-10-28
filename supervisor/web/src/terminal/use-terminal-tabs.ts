// The environment page's terminal tabs: which sessions have tabs, in the
// viewer's order, which one shows (?terminal=NAME), which panes are mounted,
// and opening, closing, ending and renaming them.
import { useCallback, useEffect, useRef, useState } from "preact/hooks";
import { api, ApiError, type Environment } from "../api.ts";
import { notify, notifyError } from "../components/toast.tsx";
import { leftNeighbour, loadOrder, orderTabs, saveOrder, vanished } from "../lib/tab-order.ts";
import { needsConfirm, others, validSession } from "./sessions.ts";

/** A × waiting for its user's go-ahead. */
export interface EndConfirmation {
  session: string;
  /** Others attached besides this page. */
  others: number;
  /** The program running in it; empty when unknown. */
  command: string;
}

export interface TerminalTabs {
  /** The sessions with tabs, in the viewer's order. */
  tabs: string[];
  /** The shown tab; null for Desktop. */
  active: string | null;
  /** Mounted panes: each tab from its first showing until it closes. */
  opened: string[];
  /** Whether a pane's first attach may create its session (an explicit open). */
  creates: (session: string) => boolean;
  /** Whether a session is still listed or pending, checked before a pane reconnects. */
  alive: (session: string) => boolean;
  /** A pane's connection state, as it last reported. */
  states: Record<string, string>;
  tabUrl: (session: string | null) => string;
  show: (session: string | null) => void;
  /** Ctrl+Shift+N: Desktop, then the tabs; false when there is no such tab or a dialog is open. */
  selectShortcut: (index: number) => boolean;
  /** Closes a session's tab; when it was the active one, moves left (or to Desktop) and maybe says so. */
  closeTab: (session: string, say: boolean, toDesktop?: boolean) => void;
  reorder: (tabs: string[]) => void;
  /** +: a new session. */
  add: () => void;
  /** ×: ends at once for an idle shell only this page uses; asks first otherwise (confirm). */
  requestEnd: (session: string) => Promise<void>;
  confirm: EndConfirmation | null;
  confirmEnd: () => void;
  cancelEnd: () => void;
  /** The session the rename dialog is open for. */
  renaming: string | null;
  startRename: (session: string) => void;
  /** The rename dialog's request: started, failed, done (with the new name), or closed. */
  rename: { onStart: () => void; onFailure: () => void; onRenamed: (next: string) => void; onClose: () => void };
  onPaneState: (session: string, state: string) => void;
  onPaneEnded: (session: string, how: "ended" | "exited") => void;
}

export function useTerminalTabs(c: Environment, query: Record<string, string>, route: (url: string, replace?: boolean) => void): TerminalTabs {
  const running = c.state === "running";
  // Observers watch the desktop: no terminals.
  const observing = c.level === "observe";
  const listed = running && !observing ? (c.terminals ?? []).map((t) => t.name) : [];
  const listKey = listed.join("\n");

  // Sessions opened here that the list doesn't show yet (session → create on first attach).
  const [pending, setPending] = useState<Record<string, boolean>>(() => {
    const s = query.terminal;
    if (!s || !validSession(s) || observing) return {};
    if (query.create === "1") return { [s]: true };
    return listed.includes(s) ? {} : { [s]: false };
  });
  // Ended from here while the list still shows them.
  const [gone, setGone] = useState<string[]>([]);
  const [opened, setOpened] = useState<string[]>([]);
  const [order, setOrder] = useState(() => loadOrder(c.name));
  const [states, setStates] = useState<Record<string, string>>({});
  const [renameInFlight, setRenameInFlight] = useState<string | null>(null);
  const [renaming, setRenaming] = useState<string | null>(null);
  const [confirm, setConfirm] = useState<EndConfirmation | null>(null);
  // Sessions listed since this page loaded, to notice one vanishing.
  const seen = useRef(new Set<string>());
  // Sessions this page ended: their end is no news.
  const ending = useRef(new Set<string>());
  // A deep link's session the list hasn't shown: if it turns out ended, Desktop shows.
  const linked = useRef(new Set<string>(!observing && query.terminal && validSession(query.terminal) && query.create !== "1" && !listed.includes(query.terminal) ? [query.terminal] : []));
  // × in flight, from asking to the end request's answer, so a double click asks or acts once.
  const busy = useRef(new Map<string, "asking" | "ending">());
  // + in flight.
  const adding = useRef(false);

  const shown = [...listed, ...(renameInFlight && !listed.includes(renameInFlight) ? [renameInFlight] : []), ...Object.keys(pending).filter((s) => !listed.includes(s))].filter((s) => !gone.includes(s));
  const tabs = orderTabs(shown, order);
  const tabsKey = tabs.join("\n");
  const active = query.terminal && tabs.includes(query.terminal) ? query.terminal : null;

  // What callbacks from panes and requests need, current.
  const latest = useRef({ tabs, shownTab: query.terminal, listed, pending, states, route });
  latest.current = { tabs, shownTab: query.terminal, listed, pending, states, route };
  // The tabs as last rendered, to find a closing tab's left neighbour.
  const rendered = useRef(tabs);
  // The tab being closed: its URL is about to change, so it isn't a bad link.
  const closing = useRef<string | null>(null);

  const tabUrl = (s: string | null) => (s ? `/${c.name}/?terminal=${encodeURIComponent(s)}` : `/${c.name}/`);
  const show = (s: string | null) => latest.current.route(tabUrl(s), true);

  const selectShortcut = useCallback((index: number) => {
    const session = index === 0 ? null : latest.current.tabs[index - 1];
    if (session === undefined || document.querySelector('[role="dialog"]')) return false;
    show(session);
    return true;
  }, []);

  const closeTab = (s: string, say: boolean, toDesktop = false) => {
    const now = latest.current;
    setOpened((o) => o.filter((x) => x !== s));
    setPending((p) => {
      if (!(s in p)) return p;
      const { [s]: _, ...rest } = p;
      return rest;
    });
    if (now.listed.includes(s)) setGone((g) => (g.includes(s) ? g : [...g, s]));
    setStates(({ [s]: _, ...rest }) => rest);
    const wasLinked = linked.current.delete(s);
    // The list and the pane may both report the end before the page moves on.
    if (now.shownTab === s && closing.current !== s) {
      // Its left neighbour among the tabs still there, or Desktop.
      const before = rendered.current.includes(s) ? rendered.current.slice(0, rendered.current.indexOf(s) + 1) : [s];
      const left = before.filter((t) => t === s || now.tabs.includes(t));
      closing.current = s;
      show(toDesktop || wasLinked ? null : leftNeighbour(left, s));
      if (say) notify(`Session ${s} ended`);
    }
  };

  // On load: drop ?create=1 (an explicit open, e.g. an environment card's +),
  // and check that a deep-linked session exists; a link never creates one.
  useEffect(() => {
    const s = query.terminal;
    if (!s) return;
    if (!validSession(s) || observing) {
      show(null);
      return;
    }
    if (query.create === "1") {
      show(s);
      return;
    }
    if (listed.includes(s) || !running) return;
    api.terminalSessions(c.name).then(
      (r) => {
        if (!r.sessions.some((t) => t.name === s)) closeTab(s, true, true);
      },
      () => undefined, // the pane's join says whether it exists
    );
  }, []);

  // Follow the session list: newly listed sessions stop being pending, and a
  // session that disappears (ended elsewhere, or its shell exited) closes.
  useEffect(() => {
    if (Object.keys(pending).some((s) => listed.includes(s))) setPending((p) => Object.fromEntries(Object.entries(p).filter(([s]) => !listed.includes(s))));
    if (gone.some((s) => !listed.includes(s))) setGone((g) => g.filter((s) => listed.includes(s)));
    for (const s of listed) {
      seen.current.add(s);
      linked.current.delete(s);
    }
    for (const s of vanished(seen.current, listed, [...Object.keys(pending), ...(renameInFlight ? [renameInFlight] : [])])) {
      seen.current.delete(s);
      closeTab(s, running && !ending.current.has(s));
    }
  }, [listKey, running]);

  // A stopped environment has no terminal tabs.
  useEffect(() => {
    if (running) return;
    setPending({});
    setOpened([]);
    setGone([]);
  }, [running]);

  // A link to a tab that isn't there (any more) shows Desktop.
  useEffect(() => {
    if (query.terminal === closing.current) return;
    closing.current = null;
    if (query.terminal && !active && !(query.terminal in pending)) show(null);
  }, [query.terminal, active, tabsKey]);

  // Panes mount on first showing.
  useEffect(() => {
    if (active && !opened.includes(active)) setOpened((o) => [...o, active]);
  }, [active]);

  // The viewer's order: new sessions go at the end of it from now on.
  useEffect(() => {
    if (!tabs.length) return;
    setOrder(tabs);
    saveOrder(c.name, tabs);
  }, [tabsKey]);

  useEffect(() => {
    rendered.current = tabs;
  });

  const reorder = (next: string[]) => {
    setOrder(next);
    saveOrder(c.name, next);
  };

  /** A session + just created (or renamed to): it exists, so its pane only joins. */
  const openCreated = (s: string) => {
    ending.current.delete(s);
    seen.current.delete(s); // a reused name: a late list without it isn't about this one
    setGone((g) => g.filter((x) => x !== s));
    setPending((p) => ({ ...p, [s]: false }));
    show(s);
  };
  const add = () => {
    if (adding.current) return;
    adding.current = true;
    api
      .createTerminal(c.name)
      .then(({ name: s }) => openCreated(s), notifyError)
      .finally(() => {
        adding.current = false;
      });
  };

  /** Ends a session, for the × that holds it in busy; releases it once answered. */
  const end = (s: string) => {
    if (busy.current.get(s) === "ending") return;
    busy.current.set(s, "ending");
    ending.current.add(s);
    api
      .endTerminal(c.name, s)
      .finally(() => busy.current.delete(s))
      .then(
        () => {
          closeTab(s, false);
          notify(`Ended ${s}`);
        },
        (e: unknown) => {
          if (e instanceof ApiError && e.status === 404) {
            closeTab(s, false);
            notify(`${s} had already ended`);
          } else {
            ending.current.delete(s);
            notifyError(e);
          }
        },
      );
  };
  const requestEnd = async (s: string) => {
    if (busy.current.has(s)) return;
    busy.current.set(s, "asking");
    // Once askEnd hands over, the dialog or end()'s request releases it.
    let handedOver = false;
    try {
      handedOver = await askEnd(s);
    } finally {
      if (!handedOver) busy.current.delete(s);
    }
  };
  /** Whether it handed the session over to the dialog or to end(). */
  const askEnd = async (s: string): Promise<boolean> => {
    let session = (c.terminals ?? []).find((t) => t.name === s);
    try {
      // The freshest details: a program may have started since the last poll.
      const r = await api.terminalSessions(c.name);
      session = r.sessions.find((t) => t.name === s);
      if (!session) {
        closeTab(s, false);
        notify(`${s} had already ended`);
        return false;
      }
    } catch {
      // the list we have
    }
    const attached = latest.current.states[s] === "connected";
    const details = session ?? { clients: attached ? 1 : 0, command: "" };
    if (needsConfirm(details, attached)) {
      setConfirm({ session: s, others: others(details, attached), command: details.command });
    } else end(s);
    return true;
  };
  const confirmEnd = () => {
    if (confirm) end(confirm.session);
    setConfirm(null);
  };
  const cancelEnd = () => {
    if (confirm) busy.current.delete(confirm.session);
    setConfirm(null);
  };

  const rename = {
    onStart: () => setRenameInFlight(renaming),
    onFailure: () => setRenameInFlight(null),
    onClose: () => setRenaming(null),
    onRenamed: (next: string) => {
      const old = renaming;
      setRenameInFlight(null);
      setRenaming(null);
      if (!old || next === old) return;
      // The old name's tab becomes the new one's, in its place.
      seen.current.delete(old);
      setOpened((o) => o.filter((s) => s !== old));
      setGone((g) => [...g, old]);
      setOrder((order) => order.map((s) => (s === old ? next : s)));
      openCreated(next);
    },
  };

  const onPaneEnded = useCallback((s: string, how: "ended" | "exited") => {
    if (how === "ended") {
      closeTab(s, !ending.current.has(s));
      return;
    }
    // The shell exited: the tab closes if that ended the session.
    api.terminalSessions(c.name).then(
      (r) => {
        if (!r.sessions.some((t) => t.name === s)) closeTab(s, true);
      },
      () => undefined,
    );
  }, []);
  const onPaneState = useCallback((s: string, state: string) => setStates((m) => (m[s] === state ? m : { ...m, [s]: state })), []);

  return {
    tabs,
    active,
    opened: opened.filter((s) => tabs.includes(s)),
    creates: (s) => pending[s] === true,
    alive: (s) => latest.current.listed.includes(s) || s in latest.current.pending,
    states,
    tabUrl,
    show,
    selectShortcut,
    closeTab,
    reorder,
    add,
    requestEnd,
    confirm,
    confirmEnd,
    cancelEnd,
    renaming,
    startRename: setRenaming,
    rename,
    onPaneState,
    onPaneEnded,
  };
}
