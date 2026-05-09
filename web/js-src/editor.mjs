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

function setConn(state, label, title) {
  connStatus.textContent = label;
  connStatus.title = title || label;
  connStatus.classList.remove("bg-success", "bg-secondary", "bg-danger", "bg-warning", "text-dark");
  switch (state) {
    case "synced":
      connStatus.classList.add("bg-success");
      break;
    case "connecting":
    case "syncing":
      connStatus.classList.add("bg-warning", "text-dark");
      break;
    case "error":
      connStatus.classList.add("bg-danger");
      break;
    default:
      connStatus.classList.add("bg-secondary");
  }
}

let syncCompleted = false;

const ydoc = new Y.Doc();
const ytext = ydoc.getText("content");

const wsProto = location.protocol === "https:" ? "wss" : "ws";
const wsBase = `${wsProto}://${location.host}/d/${docId}`;
const provider = new WebsocketProvider(wsBase, "ws", ydoc, { WebSocketPolyfill: WebSocket, connect: true });

provider.on("status", (event) => {
  if (event.status === "connected") {
    if (syncCompleted) {
      setConn("synced", "live", "WebSocket connected and document synced");
    } else {
      setConn("connecting", "syncing…", "WebSocket connected; waiting for document state");
    }
  } else if (event.status === "connecting") {
    setConn("connecting", "connecting…", "Opening WebSocket");
  } else if (event.status === "disconnected") {
    setConn("offline", "offline", "WebSocket disconnected");
  }
});

provider.on("connection-error", (err) => {
  setConn("error", "error", `WebSocket error: ${err?.message || err || "unknown"}`);
});

provider.on("sync", (isSynced) => {
  if (isSynced) {
    syncCompleted = true;
    setConn("synced", "live", "WebSocket connected and document synced");
  }
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

const cursorStorageKey = `mpn:cursor:${docId}`;

function loadSavedCursor() {
  try {
    const raw = localStorage.getItem(cursorStorageKey);
    if (!raw) return null;
    const obj = JSON.parse(raw);
    if (typeof obj?.head === "number") return obj;
  } catch {
    /* ignore */
  }
  return null;
}

function saveCursor(view) {
  const sel = view.state.selection.main;
  try {
    localStorage.setItem(cursorStorageKey, JSON.stringify({
      head: sel.head,
      anchor: sel.anchor,
    }));
  } catch {
    /* quota or disabled storage; ignore */
  }
}

let saveCursorTimer = null;
const cursorPersistPlugin = EditorView.updateListener.of((update) => {
  if (update.selectionSet || update.docChanged) {
    clearTimeout(saveCursorTimer);
    saveCursorTimer = setTimeout(() => saveCursor(update.view), 250);
  }
});

const state = EditorState.create({
  doc: "",
  extensions: [
    basicSetup,
    markdown(),
    yCollab(ytext, provider.awareness),
    EditorView.lineWrapping,
    cursorPersistPlugin,
  ],
});

const view = new EditorView({ state, parent: editorMount });

// Restore saved cursor position once the Yjs document has finished its initial sync,
// so the document length reflects the real content and the offset is meaningful.
let cursorRestored = false;
function restoreCursor() {
  if (cursorRestored) return;
  cursorRestored = true;
  const saved = loadSavedCursor();
  const docLen = view.state.doc.length;
  let head = 0;
  let anchor = 0;
  if (saved) {
    head = Math.max(0, Math.min(saved.head, docLen));
    anchor = Math.max(0, Math.min(saved.anchor ?? saved.head, docLen));
  } else {
    head = anchor = docLen;
  }
  view.dispatch({ selection: EditorSelection.single(anchor, head) });
  view.focus();
}

provider.once?.("synced", restoreCursor);
provider.on("sync", (isSynced) => { if (isSynced) restoreCursor(); });
// Fallback: focus immediately so the user can start typing even before sync completes;
// restoreCursor will reposition once sync arrives. If sync never fires (offline), still
// focus and use whatever local content exists.
view.focus();
setTimeout(restoreCursor, 1500);

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
const historyCopy = document.getElementById("historyCopy");

let historySelectedText = null;

function fmtTime(unix) {
  try {
    return new Date(unix * 1000).toLocaleString();
  } catch {
    return String(unix);
  }
}

function clearHistorySelection() {
  historySelectedText = null;
  historyCopy.classList.add("d-none");
  historyView.textContent = "";
  historyViewing.textContent = "Select a snapshot to view";
}

async function loadHistoryList() {
  clearHistorySelection();
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
  historySelectedText = null;
  historyCopy.classList.add("d-none");
  try {
    const res = await fetch(`/d/${docId}/versions/${id}.json`);
    if (!res.ok) throw new Error("failed");
    const data = await res.json();
    const text = data.text || "";
    historyView.textContent = text;
    historySelectedText = text;
    historyCopy.classList.remove("d-none");
    historyCopy.textContent = "📋";
    historyCopy.title = "Copy snapshot to clipboard";
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

async function copySelectedSnapshot() {
  if (historySelectedText == null) return;
  let ok = false;
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(historySelectedText);
      ok = true;
    } else {
      // Fallback for non-secure contexts / older browsers.
      const ta = document.createElement("textarea");
      ta.value = historySelectedText;
      ta.style.position = "fixed";
      ta.style.opacity = "0";
      document.body.appendChild(ta);
      ta.select();
      ok = document.execCommand("copy");
      ta.remove();
    }
  } catch {
    ok = false;
  }
  historyCopy.textContent = ok ? "✅" : "⚠️";
  historyCopy.title = ok ? "Copied!" : "Copy failed";
  setTimeout(() => {
    historyCopy.textContent = "📋";
    historyCopy.title = "Copy snapshot to clipboard";
  }, 1200);
}

historyPanel?.addEventListener("show.bs.offcanvas", loadHistoryList);
historyRefresh?.addEventListener("click", loadHistoryList);
historyCopy?.addEventListener("click", copySelectedSnapshot);

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
