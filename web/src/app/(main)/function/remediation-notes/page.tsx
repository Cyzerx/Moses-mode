"use client";

import { useEffect, useState } from "react";

import { Input } from "@/components/ui/input";
import { api, type ReviewerNote } from "@/lib/api";

export default function RemediationNotes() {
  const [notes, setNotes] = useState<ReviewerNote[]>([]);
  const [status, setStatus] = useState<"loading" | "ready" | "error">("loading");
  const [query, setQuery] = useState("");
  const [includeRetired, setIncludeRetired] = useState(false);
  useEffect(() => {
    let active = true;
    api
      .reviewerNotes()
      .then((data) => {
        if (active) {
          setNotes(data);
          setStatus("ready");
        }
      })
      .catch(() => {
        if (active) setStatus("error");
      });
    return () => {
      active = false;
    };
  }, []);
  const terms = query.toLowerCase().trim().split(/\s+/).filter(Boolean);
  const matches = notes.filter((n) => {
    const text = [n.title, n.guidance, n.source, ...(n.tags ?? [])].join(" ").toLowerCase();
    return (includeRetired || n.status === "current") && terms.every((t) => text.includes(t));
  });
  return (
    <main className="mx-auto w-full max-w-4xl space-y-5 p-5">
      <h1 className="text-2xl font-semibold">Remediation notes</h1>
      <Input
        aria-label="Search remediation notes"
        placeholder="Search notes or tags"
        value={query}
        onChange={(e) => setQuery(e.target.value)}
      />
      <label className="flex items-center gap-2 text-sm">
        <input type="checkbox" checked={includeRetired} onChange={(e) => setIncludeRetired(e.target.checked)} />
        Include retired notes
      </label>
      {status === "loading" && <p role="status">Loading notes…</p>}
      {status === "error" && <p role="alert">The reviewer catalogue could not be loaded.</p>}
      {status === "ready" && matches.length === 0 && <p>No matching notes.</p>}
      {matches.map((n) => (
        <article key={JSON.stringify([n.title, n.source, n.reviewed])} className="space-y-2 rounded-md border p-4">
          <h2 className="text-lg font-semibold">
            {n.title}
            {n.status === "retired" && " (retired)"}
          </h2>
          <p>{n.guidance}</p>
          <p className="text-muted-foreground text-sm">{(n.tags ?? []).join(" · ")}</p>
          <p className="text-muted-foreground text-sm">
            {n.source} · {n.owner} · Reviewed {n.reviewed}
          </p>
        </article>
      ))}
    </main>
  );
}
