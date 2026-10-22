// Renders the shared top bar and the wall of computers, so the dialog
// mockups can show it dimmed behind them.
const computers = [
  { name: "alice", state: "run", status: "Running for 3 days", img: "alice", cpu: 34, mem: [1.4, 2], viewers: ["zef", "anna"], image: "Debian XFCE 2026.10.15", update: true, access: ["anna"] },
  { name: "research", state: "run", status: "Running for 5 hours", img: "research", cpu: 8, mem: [0.9, 2], viewers: ["anna"], image: "Debian XFCE 2026.10.22", access: ["anna"] },
  { name: "build-bot", state: "run", status: "Running for 12 minutes", img: "build-bot", cpu: 91, mem: [3.1, 4], viewers: [], image: "Debian XFCE 2026.10.22" },
  { name: "notes", state: "run", status: "Running for 2 days", img: "notes", cpu: 2, mem: [0.7, 2], viewers: [], image: "Debian XFCE 2026.10.15", update: true, access: ["anna"] },
  { name: "alice-2", state: "busy", status: "Cloning from alice", progress: 38, progressText: "Copying files, 1.3 of 3.4 GB" },
  { name: "scratch", state: "off", status: "Stopped 4 days ago", mem: [0, 1], image: "Debian XFCE 2026.10.15" },
];

const colors = { zef: "c1", anna: "c2", sam: "c3", ci: "c4" };
function av(name) { return `<span class="av ${colors[name] || "c1"}" title="${name}">${name[0]}</span>`; }

// Who the mockup is signed in as: an admin by default, a regular account
// when the page sets `const viewer = "anna"` before loading this script.
const me = typeof viewer === "string" ? viewer : "zef";
const isAdmin = me === "zef";

function topBar(page = "computers") {
  const nav = isAdmin ? `
    <nav class="nav">
      <a href="computers.html" class="${page === "computers" ? "on" : ""}">Computers</a>
      <a href="images.html" class="${page === "images" ? "on" : ""}">Images</a>
      <a href="accounts.html" class="${page === "accounts" ? "on" : ""}">Accounts</a>
    </nav>` : "";
  const host = isAdmin ? `
      <span><strong>4</strong> of 6 running</span>
      <span>Memory <strong>6.1</strong> of 16 GB</span>
      <div class="meter" style="width:96px"><i style="width:38%"></i></div>` : "";
  return `
  <div class="top">
    <a class="wordmark" href="computers.html">container-computer <span>supervisor</span></a>
    ${nav}
    <div class="host">${host}
      <span class="account">${av(me)} ${me}${isAdmin ? ' <span class="role">admin</span>' : ""}</span>
    </div>
  </div>`;
}

function visibleComputers() {
  return isAdmin ? computers : computers.filter((c) => (c.access || []).includes(me));
}

function viewersBadge(c) {
  if (!c.viewers || !c.viewers.length) return "";
  const names = c.viewers.length === 1 ? c.viewers[0] : `${c.viewers.length} viewing`;
  return `<span class="viewers"><span class="avs">${c.viewers.map(av).join("")}</span>${names}</span>`;
}

function tile(c) {
  let screen;
  if (c.state === "run") {
    screen = `<img src="img/${c.img}.png" alt="">${viewersBadge(c)}<span class="open">Open desktop</span>`;
  } else if (c.state === "busy") {
    screen = `<div class="blank"><div class="progress"><i style="width:${c.progress}%"></i></div>${c.progressText}</div>`;
  } else {
    screen = `<div class="blank">Stopped<button class="btn primary">Start</button></div>`;
  }
  const actions = c.state === "busy" ? "" : `<button class="btn icon" aria-label="More actions">⋯</button>`;
  const cls = c.state === "run" ? "run" : c.state === "busy" ? "busy" : "";
  const meters = c.state === "busy" ? "" : c.state === "off" ? `<div class="limits">${c.mem[1]} GB memory, 2 CPUs</div>` : `
    <div class="meters">
      <div><div class="meter-label">CPU <b>${c.cpu}%</b></div>
        <div class="meter"><i class="${c.cpu > 80 ? "hot" : ""}" style="width:${c.cpu || 0}%"></i></div></div>
      <div><div class="meter-label">Memory <b>${c.mem[0]} of ${c.mem[1]} GB</b></div>
        <div class="meter"><i style="width:${(100 * c.mem[0]) / c.mem[1]}%"></i></div></div>
    </div>`;
  const image = c.image ? `<div class="tile-image">${c.image}${c.update && isAdmin ? ' · <span class="tag update">Update available</span>' : ""}</div>` : "";
  return `
  <article class="tile ${c.state}" data-name="${c.name}">
    <a href="#desktop-${c.name}" class="monitor" title="Open ${c.name}'s desktop"><div class="screen">${screen}</div></a>
    <div class="tile-meta">
      <div class="tile-row">
        <div class="who-what"><h2><a href="computer.html">${c.name}</a></h2>
        <span class="status ${cls}">${c.status}</span></div>
        <div class="actions">${actions}</div>
      </div>
      ${meters}
      ${image}
    </div>
  </article>`;
}

function renderWall() {
  document.getElementById("top").innerHTML = topBar();
  document.getElementById("wall").innerHTML = visibleComputers().map(tile).join("");
}
