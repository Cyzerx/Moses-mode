"use client";

import * as React from "react";
import { Button } from "@/components/ui/button";
import { api } from "@/lib/api";

export function TaskIntegrity({ taskId }: { taskId: string }) {
  const [databaseOnly, setDatabaseOnly] = React.useState(false);
  const [result, setResult] = React.useState<Awaited<ReturnType<typeof api.taskIntegrity>>>();
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState("");
  const active = React.useRef<AbortController | undefined>(undefined);
  React.useEffect(() => () => active.current?.abort(), []);

  async function verify() {
    active.current?.abort();
    const controller = new AbortController();
    active.current = controller;
    setBusy(true);
    setError("");
    setResult(undefined);
    try {
      const next = await api.taskIntegrity(taskId, databaseOnly, controller.signal);
      if (!controller.signal.aborted) setResult(next);
    } catch (reason) {
      if (!controller.signal.aborted) setError((reason as Error).message);
    } finally {
      if (!controller.signal.aborted) setBusy(false);
    }
  }

  return (
    <div className="mt-4 grid gap-2 border-t pt-3 text-sm">
      <div className="flex flex-wrap items-center gap-3">
        <Button size="sm" variant="outline" onClick={verify} disabled={busy}>
          {busy ? "Checking…" : "Check engagement integrity"}
        </Button>
        <label className="flex items-center gap-2">
          <input type="checkbox" checked={databaseOnly} disabled={busy} onChange={(event) => {
            setDatabaseOnly(event.target.checked);
            setResult(undefined);
            setError("");
          }} />
          Database-only restore (skip evidence files)
        </label>
      </div>
      {error && <p role="alert">{error}</p>}
      {result && <div role="status">
        <p>{result.problems} integrity problems · {result.stale_reports} stale reports · {result.unlinked_findings} unlinked historical records.</p>
        <p>{result.bindings} evidence bindings · {result.bodies_checked} distinct bodies checked · {result.bodies_skipped} skipped.</p>
        {result.issues.length > 0 && <details className="mt-2">
          <summary>Record details (up to 100 per category)</summary>
          <ul className="ml-5 list-disc">{result.issues.map((item, index) => (
            <li key={index}>{item.code.replaceAll("_", " ")} · {item.record} #{item.id}
              {item.related && ` → #${item.related}`}{item.side && ` · ${item.side}`}</li>
          ))}</ul>
        </details>}
      </div>}
    </div>
  );
}
