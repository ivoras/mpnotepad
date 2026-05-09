import * as Y from "yjs";
import { WebsocketProvider } from "y-websocket";
import { yCollab } from "y-codemirror.next";
import { EditorView, basicSetup } from "codemirror";
import { EditorState } from "@codemirror/state";
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
