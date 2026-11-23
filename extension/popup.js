const $ = (id) => document.getElementById(id);

async function refreshState() {
  const st = await chrome.runtime.sendMessage({ type: "getState" });
  const lines = [];
  lines.push(st.capturing
    ? `● Recording tab ${st.tabId}`
    : "○ Not recording");
  lines.push(`ingested: ${st.ingested} · queued: ${st.queued} · failed: ${st.failed}`);
  if (st.destination && (st.capturing || st.queued)) {
    lines.push("Recipient: " + st.destination, "Engagement: " + (st.engagement || "operator only"), "Origins: " + st.origins.join(", "));
  }
  $("discard").hidden = st.capturing || !st.queued;
  if (st.lastError) lines.push(st.lastError);
  $("status").textContent = lines.join("\n");

}

let savedRecipient = "";
$("serverUrl").addEventListener("input", () => {
  if ($("serverUrl").value.trim() !== savedRecipient) $("token").value = "";
});

$("save").addEventListener("click", async () => {
  if (!$("token").value.trim()) {
    $("error").textContent = "Enter a token for this Artex-Moses-mode recipient.";
    return;
  }
  savedRecipient = $("serverUrl").value.trim();
  $("error").textContent = "";
  await chrome.storage.local.set({
    serverUrl: $("serverUrl").value.trim(),
    token: $("token").value.trim(),
    taskId: $("taskId").value.trim(),
    origins: $("origins").value.trim(),
  });
  $("save").textContent = "Saved ✓";
  setTimeout(() => ($("save").textContent = "Save settings"), 1200);
});

$("start").addEventListener("click", async () => {
  const out = await chrome.runtime.sendMessage({ type: "start" });
  $("error").textContent = out?.ok === false ? out.error : "";
  refreshState();
});

$("stop").addEventListener("click", async () => {
  await chrome.runtime.sendMessage({ type: "stop" });
  refreshState();
});

$("flush").addEventListener("click", async () => {
  await chrome.runtime.sendMessage({ type: "flushNow" });
  refreshState();
});

$("discard").addEventListener("click", async () => {
  if (confirm("Discard the unsent evidence from this recording?")) {
    const out = await chrome.runtime.sendMessage({ type: "discard" });
    $("error").textContent = out?.error || "";
    refreshState();
  }
});

(async () => {
  const { serverUrl = "", token = "", taskId = "", origins = "" } = await chrome.storage.local.get(["serverUrl", "token", "taskId", "origins"]);
  savedRecipient = serverUrl;
  $("taskId").value = taskId;
  $("origins").value = origins;
  $("serverUrl").value = serverUrl;
  $("token").value = token;
  refreshState();
  setInterval(refreshState, 1500);
})();
