import * as Y from "https://esm.sh/yjs@13.6.27";
import { WebsocketProvider } from "https://esm.sh/y-websocket@2.0.4?deps=yjs@13.6.27";
import { yCollab } from "https://esm.sh/y-codemirror.next@0.3.5?deps=yjs@13.6.27,@codemirror/state@6.4.1,@codemirror/view@6.28.0";
import { EditorView, basicSetup } from "https://esm.sh/codemirror@6.0.1?deps=@codemirror/state@6.4.1,@codemirror/view@6.28.0,@codemirror/commands@6.6.2,@codemirror/language@6.10.0,@codemirror/search@6.5.6,@codemirror/autocomplete@6.18.0,@codemirror/lint@6.8.0";
import { EditorState } from "https://esm.sh/@codemirror/state@6.4.1";
import { markdown } from "https://esm.sh/@codemirror/lang-markdown@6.3.0";
import { marked } from "https://esm.sh/marked@12.0.2";

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

const view = new EditorView({ state, parent: editorMount });

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
