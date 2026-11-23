"use client";

import * as React from "react";

import { Button } from "@/components/ui/button";
import { api } from "@/lib/api";

export function OfflineFindingReview({ taskId, ids }: { taskId: string; ids: string[] }) {
  const [titles, setTitles] = React.useState(false);
  const [preview, setPreview] = React.useState<{ url: string; key: string }>();
  const [error, setError] = React.useState("");
  const selection = ids.join(",");
  const key = `${taskId}|${selection}|${titles}`;
  const valid = /^[1-9]\d*$/.test(taskId) && ids.length > 0 && ids.length <= 100;

  React.useEffect(() => {
    setError("");
    setPreview(undefined);
    if (!valid) return;
    const controller = new AbortController();
    let url: string | undefined;
    api.reviewFindings(taskId, selection.split(","), titles, controller.signal)
      .then((blob) => {
        if (controller.signal.aborted) return;
        url = URL.createObjectURL(blob);
        setPreview({ url, key });
      })
      .catch((reason: Error) => {
        if (!controller.signal.aborted) setError(reason.message);
      });
    return () => {
      controller.abort();
      if (url) URL.revokeObjectURL(url);
    };
  }, [key, selection, taskId, titles, valid]);

  if (!valid) return <p className="text-sm">Filter by one task and select 1–100 findings from that task.</p>;
  const url = preview?.key === key ? preview.url : undefined;
  return (
    <div className="grid gap-2 text-sm">
      <label className="flex items-center gap-2">
        <input type="checkbox" checked={titles} onChange={(event) => setTitles(event.target.checked)} />
        Include finding titles
      </label>
      <p>Review the exact file below before sharing it. Titles may contain client data.</p>
      {error ? <p role="alert">{error}</p> : url ? (
        <>
          <iframe title="Offline evidence file preview" src={url} sandbox="" className="h-56 w-full rounded border" />
          <Button asChild>
            <a href={url} download="selected-finding-evidence.html">Download reviewed HTML</a>
          </Button>
        </>
      ) : <p role="status">Loading preview…</p>}
    </div>
  );
}
