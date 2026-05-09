import * as Y from "yjs";
import { WebsocketProvider } from "y-websocket";
import { yCollab } from "y-codemirror.next";
import { EditorView, basicSetup } from "codemirror";
import { EditorState, EditorSelection } from "@codemirror/state";
import { markdown } from "@codemirror/lang-markdown";
import { marked } from "marked";

function metaContent(name) {
  const el = document.querySelector(`meta[name="${name}"]`);
  return el ? el.getAttribute("content") || "" : "";
}

const docId = metaContent("mpn-doc-id");
const markdownPanel = metaContent("mpn-markdown-panel") === "1";

const titleInput = document.getElementById("docTitle");
const connStatus = document.getElementById("connStatus");
const previewEl = document.getElementById("preview");
const previewPane = document.getElementById("previewPane");
const editorMount = document.getElementById("editor");

const setTitle = document.getElementById("setTitle");
const setMarkdown = document.getElementById("setMarkdown");
const setPassword = document.getElementById("setPassword");
const setClearPassword = document.getElementById("setClearPassword");
const saveSettings = document.getElementById("saveSettings");
const settingsError = document.getElementById("settingsError");

function setConn(ok, label) {
  connStatus.textContent = label;
  connStatus.classList.toggle("bg-success", ok);
  connStatus.classList.toggle("bg-secondary", !ok);
  connStatus.classList.toggle("bg-danger", !ok && label === "error");
}

const ydoc = new Y.Doc();
const ytext = ydoc.getText("content");

const wsProto = location.protocol === "https:" ? "wss" : "ws";
const wsBase = `${wsProto}://${location.host}/d/${docId}`;
const provider = new WebsocketProvider(wsBase, "ws", ydoc, { WebSocketPolyfill: WebSocket, connect: true });

provider.on("status", (event) => {
  if (event.status === "connected") {
    setConn(true, "live");
  } else if (event.status === "disconnected") {
    setConn(false, "offline");
  }
});

provider.on("connection-error", () => {
  setConn(false, "error");
});

if (markdownPanel) {
  previewPane.classList.remove("d-none");
}

let previewTimer = null;
function schedulePreview() {
  if (!markdownPanel) return;
  clearTimeout(previewTimer);
  previewTimer = setTimeout(() => {
    previewEl.innerHTML = marked.parse(ytext.toString());
  }, 120);
}

ytext.observe(schedulePreview);
schedulePreview();

// Push a flattened plaintext snapshot to the server over the websocket text channel.
// The hub uses the most-recent snapshot to write a row in document_versions whenever
// a client disconnects, so the server never needs to decode Yjs state itself.
let snapshotTimer = null;
let lastSnapshotSent = null;
function sendTextSnapshot() {
  const ws = provider.ws;
  if (!ws || ws.readyState !== WebSocket.OPEN) return;
  const text = ytext.toString();
  if (text === lastSnapshotSent) return;
  try {
    ws.send(JSON.stringify({ type: "text-snapshot", text }));
    lastSnapshotSent = text;
  } catch {
    /* ignore transient send errors; next change will retry */
  }
}
function scheduleSnapshot() {
  clearTimeout(snapshotTimer);
  snapshotTimer = setTimeout(sendTextSnapshot, 600);
}
ytext.observe(scheduleSnapshot);
provider.on("status", (event) => {
  if (event.status === "connected") {
    // Send an initial snapshot once the socket comes up so even read-only sessions
    // produce a checkpoint on disconnect.
    setTimeout(sendTextSnapshot, 250);
  }
});

const state = EditorState.create({
  doc: "",
  extensions: [
    basicSetup,
    markdown(),
    yCollab(ytext, provider.awareness),
    EditorView.lineWrapping,
  ],
});

new EditorView({ state, parent: editorMount });

titleInput?.addEventListener("input", () => {
  document.title = titleInput.value.trim() || "Untitled";
});

titleInput?.addEventListener("blur", async () => {
  const t = titleInput.value.trim();
  try {
    await fetch(`/d/${docId}/settings`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        title: t,
        markdown_panel: markdownPanel,
        password: "",
        clear_password: false,
      }),
    });
  } catch {
    /* ignore */
  }
});

document.getElementById("settingsModal")?.addEventListener("show.bs.modal", async () => {
  settingsError.classList.add("d-none");
  try {
    const res = await fetch(`/d/${docId}/settings.json`);
    if (!res.ok) throw new Error("failed to load settings");
    const data = await res.json();
    setTitle.value = data.title || "";
    setMarkdown.checked = !!data.markdown_panel;
    setPassword.value = "";
    setClearPassword.checked = false;
  } catch (e) {
    settingsError.textContent = "Could not load settings.";
    settingsError.classList.remove("d-none");
  }
});

const historyPanel = document.getElementById("historyPanel");
const historyList = document.getElementById("historyList");
const historyEmpty = document.getElementById("historyEmpty");
const historyView = document.getElementById("historyView");
const historyViewing = document.getElementById("historyViewing");
const historyRefresh = document.getElementById("historyRefresh");

function fmtTime(unix) {
  try {
    return new Date(unix * 1000).toLocaleString();
  } catch {
    return String(unix);
  }
}

async function loadHistoryList() {
  historyList.innerHTML = "";
  historyEmpty.textContent = "Loading...";
  historyList.appendChild(historyEmpty);
  try {
    const res = await fetch(`/d/${docId}/versions.json`);
    if (!res.ok) throw new Error("failed");
    const data = await res.json();
    const versions = data.versions || [];
    historyList.innerHTML = "";
    if (versions.length === 0) {
      const empty = document.createElement("div");
      empty.className = "text-muted small p-3";
      empty.textContent = "No snapshots yet. One will be saved when a collaborator disconnects.";
      historyList.appendChild(empty);
      return;
    }
    for (const v of versions) {
      const a = document.createElement("button");
      a.type = "button";
      a.className = "list-group-item list-group-item-action d-flex justify-content-between align-items-start";
      const left = document.createElement("div");
      const title = document.createElement("div");
      title.className = "fw-semibold small";
      title.textContent = fmtTime(v.created_at);
      const sub = document.createElement("div");
      sub.className = "text-muted small";
      sub.textContent = `${v.char_count} chars · ${v.byte_size} B`;
      left.appendChild(title);
      left.appendChild(sub);
      a.appendChild(left);
      a.addEventListener("click", () => loadHistoryVersion(v.id, v.created_at));
      historyList.appendChild(a);
    }
  } catch {
    historyList.innerHTML = "";
    const err = document.createElement("div");
    err.className = "text-danger small p-3";
    err.textContent = "Could not load history.";
    historyList.appendChild(err);
  }
}

async function loadHistoryVersion(id, createdAt) {
  historyView.textContent = "Loading...";
  historyViewing.textContent = `Snapshot from ${fmtTime(createdAt)}`;
  try {
    const res = await fetch(`/d/${docId}/versions/${id}.json`);
    if (!res.ok) throw new Error("failed");
    const data = await res.json();
    historyView.textContent = data.text || "";
    Array.from(historyList.children).forEach((el) => el.classList.remove("active"));
    const items = historyList.querySelectorAll("button");
    items.forEach((el) => {
      if (el.textContent && el.textContent.includes(fmtTime(createdAt))) {
        el.classList.add("active");
      }
    });
  } catch {
    historyView.textContent = "Could not load snapshot.";
  }
}

historyPanel?.addEventListener("show.bs.offcanvas", loadHistoryList);
historyRefresh?.addEventListener("click", loadHistoryList);

saveSettings?.addEventListener("click", async () => {
  settingsError.classList.add("d-none");
  const body = {
    title: setTitle.value,
    markdown_panel: setMarkdown.checked,
    password: setPassword.value,
    clear_password: setClearPassword.checked,
  };
  try {
    const res = await fetch(`/d/${docId}/settings`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    if (!res.ok) throw new Error("save failed");
    if (body.clear_password || body.password) {
      window.location.reload();
      return;
    }
    titleInput.value = body.title;
    document.title = body.title.trim() || "Untitled";
    if (body.markdown_panel !== markdownPanel) {
      window.location.reload();
      return;
    }
    bootstrap.Modal.getInstance(document.getElementById("settingsModal"))?.hide();
  } catch (e) {
    settingsError.textContent = "Save failed.";
    settingsError.classList.remove("d-none");
  }
});
