// Artex-Moses-mode Traffic Capture — background service worker.
//
// Captures the network activity of one tab through the chrome.debugger API
// (Network domain) and pushes text request/response pairs to the Artex-Moses-mode
// ingest endpoint in batches. State lives in chrome.storage.session so the
// popup and the (short-lived) service worker share one view; the queue is
// deliberately in-memory — a browser restart drops unsent entries, which is
// the acceptable failure mode for a manual capture session.

const FLUSH_AT = 10; // entries per batch
const MAX_BODY = 256 * 1024; // matches the server-side ingest cap
const SKIP_MIME = ["image/", "font/", "audio/", "video/", "application/octet-stream"];

let session = {
  capturing: false,
  tabId: null,
  queue: [], // pending ExternalExchange objects
  requests: new Map(), // debugger requestId -> partial request info
  ingested: 0,
  failed: 0,
  lastError: "",
};

async function saveState() {
  await chrome.storage.session.set({
    capturing: session.capturing,
    tabId: session.tabId,
    ingested: session.ingested,
    failed: session.failed,
    lastError: session.lastError,
    queued: session.queue.length,
  });
}

async function getConfig() {
  return chrome.storage.local.get(["serverUrl", "token", "taskId", "origins"]);
}

function httpURL(value) {
  const url = new URL(value);
  if (!["http:", "https:"].includes(url.protocol) || url.username || url.password) {
    throw new Error("Enter an HTTP or HTTPS address without credentials.");
  }
  return url;
}

async function start() {
  if (session.capturing) return { ok: true };
  if (session.queue.length || session.flushing) {
    return { ok: false, error: "Flush or discard the previous recording before starting another." };
  }
  const config = await getConfig();
  const tab = (await chrome.tabs.query({ active: true, currentWindow: true }))[0];
  if (tab?.id == null || !config.serverUrl || !config.token) {
    return { ok: false, error: "Open an HTTP(S) tab and set the Artex-Moses-mode URL and token first." };
  }
  const server = httpURL(config.serverUrl);
  if (server.search || server.hash) throw new Error("Remove the query and fragment from the Artex-Moses-mode URL.");
  const allowed = new Set([httpURL(tab.url).origin]);
  for (const entry of (config.origins || "").split(/\s+/).filter(Boolean)) {
    const url = httpURL(entry);
    if (url.pathname !== "/" || url.search || url.hash) throw new Error("Additional origins must contain only scheme, host and port.");
    allowed.add(url.origin);
  }
  const taskID = Number(config.taskId || 0);
  if (!Number.isSafeInteger(taskID) || taskID < 0) throw new Error("Enter a positive engagement ID or leave it empty.");
  const tabId = tab.id;
  const recipient = server.href.replace(/\/+$/, "");
  await chrome.debugger.attach({ tabId }, "1.3");
  try {
    await chrome.debugger.sendCommand({ tabId }, "Network.enable", {});
  } catch (e) {
    await chrome.debugger.detach({ tabId });
    throw e;
  }
  session = { capturing: true, tabId, allowed, recipient, token: config.token, taskID,
    queue: [], requests: new Map(), ingested: 0, failed: 0, lastError: "", flushing: null };
  chrome.action.setBadgeText({ text: "REC" });
  chrome.action.setBadgeBackgroundColor({ color: "#c2410c" });
  await saveState();
  return { ok: true };
}

async function stop() {
  const current = session;
  if (current.capturing && current.tabId != null) {
    current.capturing = false;
    try {
      await chrome.debugger.detach({ tabId: current.tabId });
    } catch (_) { /* the tab may have closed */ }
    current.tabId = null;
    current.requests.clear();
    await flush();
  }
  chrome.action.setBadgeText({ text: "" });
  await saveState();
  return { ok: true };
}

function skipBody(mimeType) {
  const mt = (mimeType || "").toLowerCase();
  return SKIP_MIME.some((p) => mt.startsWith(p));
}

function truncate(s) {
  if (s.length <= MAX_BODY) return s;
  return s.slice(0, MAX_BODY) + "\n... [truncated by extension]";
}

async function queueExchange(current, info, response) {
  const respHeaders = {};
  // CDP hands back response.headers as a plain object (dict), not an array of
  // {name,value} records; iterating it with for...of throws "object is not
  // iterable" and no exchange is ever queued. Accept both shapes.
  const rawHeaders = response.headers || {};
  if (Array.isArray(rawHeaders)) {
    for (const h of rawHeaders) {
      if (h && h.name != null) respHeaders[String(h.name).toLowerCase()] = h.value;
    }
  } else {
    for (const [k, v] of Object.entries(rawHeaders)) respHeaders[String(k).toLowerCase()] = v;
  }
  const mime = respHeaders["content-type"] || "";

  let reqBody = info.postData || "";
  let respBody = "";
  if (!skipBody(mime)) {
    try {
      const body = await chrome.debugger.sendCommand(
        { tabId: current.tabId },
        "Network.getResponseBody",
        { requestId: info.requestId }
      );
      if (body && !body.base64Encoded) {
        reqBody = truncate(reqBody);
        respBody = truncate(body.body || "");
      }
    } catch (_) {
      // body already discarded by the browser; capture the pair without bodies
    }
  }

  if (session !== current || !current.capturing) return;
  current.queue.push({
    method: info.method,
    url: info.url,
    status: response.status || 0,
    requestHeaders: info.headers || {},
    requestBody: reqBody,
    responseHeaders: respHeaders,
    responseBody: respBody,
  });
  if (current.queue.length >= FLUSH_AT) await flush();
}

async function flush() {
  const current = session;
  if (current.flushing) return current.flushing;
  if (!current.queue.length) return;
  current.flushing = sendQueued(current);
  try {
    await current.flushing;
  } finally {
    current.flushing = null;
    await saveState();
  }
}

async function sendQueued(current) {
  while (current.queue.length) {
    const batch = current.queue.splice(0, FLUSH_AT);
    try {
      const res = await fetch(current.recipient + "/api/traffic/ingest", {
        method: "POST",
        redirect: "error",
        credentials: "omit",
        headers: { Authorization: "Bearer " + current.token, "Content-Type": "application/json" },
        body: JSON.stringify({ task_id: current.taskID, exchanges: batch }),
      });
      if (!res.ok) throw new Error("Capture upload returned HTTP " + res.status);
      const out = await res.json();
      current.ingested += out.ingested || 0;
      current.failed += out.failed || 0;
      current.lastError = out.failed ? out.failed + " entries rejected by the server" : "";
    } catch (e) {
      current.queue = batch.concat(current.queue).slice(0, 200);
      current.lastError = e.message;
      return;
    }
  }
}

chrome.debugger.onEvent.addListener((source, method, params) => {
  if (!session.capturing || !source.tabId || source.tabId !== session.tabId) return;
  if (method === "Network.requestWillBeSent") {
    // A redirect reuses the debugger request ID, possibly on another origin.
    session.requests.delete(params.requestId);
    let origin;
    try { origin = new URL(params.request.url).origin; } catch (_) { return; }
    if (!session.allowed.has(origin)) return;
    session.requests.set(params.requestId, {
      requestId: params.requestId,
      method: params.request.method,
      url: params.request.url,
      headers: params.request.headers,
      postData: params.request.postData || "",
    });
  } else if (method === "Network.responseReceived") {
    const info = session.requests.get(params.requestId);
    if (info) {
      let origin;
      try { origin = new URL(params.response.url).origin; } catch (_) { /* skip */ }
      if (session.allowed.has(origin)) info.response = params.response;
      else session.requests.delete(params.requestId);
    }
  } else if (method === "Network.loadingFinished") {
    const info = session.requests.get(params.requestId);
    session.requests.delete(params.requestId);
    if (!info || !info.response) return;
    queueExchange(session, info, info.response).catch((e) => {
      session.lastError = e.message;
      saveState();
    });
  }
});

chrome.debugger.onDetach.addListener((source) => {
  if (session.capturing && source.tabId === session.tabId) {
    // DevTools opened, or the tab navigated somewhere undebaggable: stop
    // cleanly instead of silently dropping events.
    session.capturing = false;
    session.tabId = null;
    session.lastError = "debugger detached (DevTools opened?)";
    chrome.action.setBadgeText({ text: "" });
    saveState();
  }
});

chrome.runtime.onMessage.addListener((msg, _sender, sendResponse) => {
  (async () => {
    switch (msg?.type) {
      case "getState": {
        sendResponse({
          capturing: session.capturing,
          tabId: session.tabId,
          ingested: session.ingested,
          failed: session.failed,
          queued: session.queue.length,
          lastError: session.lastError,
          destination: session.recipient || "",
          engagement: session.taskID || 0,
          origins: [...(session.allowed || [])],
          configured: !!(await getConfig()).serverUrl,
        });
        return;
      }
      case "discard":
        if (session.capturing || session.flushing) throw new Error("Stop the recording before discarding its pending batch.");
        session.queue = [];
        session.requests.clear();
        session.lastError = "";
        await saveState();
        sendResponse({ ok: true });
        return;
      case "start":
        sendResponse(await start());
        return;
      case "stop":
        sendResponse(await stop());
        return;
      case "flushNow":
        await flush();
        sendResponse(await stateSnapshot());
        return;
      default:
        sendResponse({ ok: false, error: "unknown message" });
    }
  })().catch((err) => sendResponse({ ok: false, error: err.message }));
  return true; // async sendResponse
});

async function stateSnapshot() {
  return {
    capturing: session.capturing,
    tabId: session.tabId,
    ingested: session.ingested,
    failed: session.failed,
    queued: session.queue.length,
    lastError: session.lastError,
  };
}
