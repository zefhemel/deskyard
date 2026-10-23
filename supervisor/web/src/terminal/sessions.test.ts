import assert from "node:assert/strict";
import { test } from "node:test";
import { SessionDiscovery } from "./sessions.ts";

const tick = () => new Promise<void>((resolve) => setImmediate(resolve));
test("discovery refreshes live and empty lists, retaining entries on transient failure", async () => {
  const states: {names: string[]; loading: boolean; error: string}[] = [];
  let result: string[] | Error = ["main", "terminal-1"];
  const discovery = new SessionDiscovery(async () => {
    if (result instanceof Error) throw result;
    return result;
  }, s => states.push(s));
  discovery.start();
  await tick();
  assert.deepEqual(states.at(-1)?.names, ["main", "terminal-1"]);
  result = new Error("temporarily unavailable");
  await discovery.refresh();
  assert.deepEqual(states.at(-1), {names: ["main", "terminal-1"], loading: false, error: "temporarily unavailable"});
  result = [];
  await discovery.refresh();
  assert.deepEqual(states.at(-1), {names: [], loading: false, error: ""});
  discovery.stop();
});
test("closing cancels requests and stale results cannot overwrite reopened picker", async () => {
  const requests: {signal: AbortSignal; resolve: (names: string[]) => void}[] = [];
  let names: string[] = [];
  const discovery = new SessionDiscovery(signal => new Promise(resolve => requests.push({signal, resolve})), s => {names = s.names;});
  discovery.start();
  void discovery.refresh();
  assert.equal(requests.length, 1, "no overlapping refresh requests");
  discovery.stop();
  assert.equal(requests[0]!.signal.aborted, true);
  discovery.start();
  requests[1]!.resolve(["terminal-2"]);
  await tick();
  requests[0]!.resolve(["stale"]);
  await tick();
  assert.deepEqual(names, ["terminal-2"]);
  discovery.stop();
});
