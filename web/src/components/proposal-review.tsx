"use client";

import { useEffect, useState } from "react";

import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Textarea } from "@/components/ui/textarea";
import { api, type ReviewableProposal } from "@/lib/api";

export function ProposalReviewPanel({ taskId }: { taskId: string }) {
  const [expanded, setExpanded] = useState(false);
  const [rows, setRows] = useState<ReviewableProposal[]>([]);
  const [selected, setSelected] = useState("");
  const [draft, setDraft] = useState("");
  const [editing, setEditing] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [refresh, setRefresh] = useState(0);
  const proposal = rows.find((r) => r.id === selected);
  const review = proposal?.review;
  const canReview = proposal?.state === "open" || proposal?.state === "paused";

  // biome-ignore lint/correctness/useExhaustiveDependencies: the refresh button explicitly repeats this query.
  useEffect(() => {
    if (!expanded) return;
    let active = true;
    setBusy(true);
    setError("");
    api
      .reviewableProposals(taskId)
      .then((data) => {
        if (active) setRows(data);
      })
      .catch((e: Error) => {
        if (active) {
          setError(e.message);
          setRows([]);
        }
      })
      .finally(() => {
        if (active) setBusy(false);
      });
    return () => {
      active = false;
    };
  }, [taskId, expanded, refresh]);

  async function decide(action: "require" | "approve" | "edit" | "create") {
    setBusy(true);
    setError("");
    try {
      const result = await api.reviewProposal(taskId, action === "create" ? "" : selected, {
        action,
        revision: review?.revision ?? 0,
        summary: draft,
      });
      setRows((prev) => [result, ...prev.filter((r) => r.id !== result.id)]);
      setSelected(result.id);
      setDraft("");
      setEditing(false);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Review failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card>
      <CardContent>
        <details open={expanded} onToggle={(e) => setExpanded(e.currentTarget.open)}>
          <summary className="cursor-pointer text-sm font-semibold">Proposal review</summary>
          {expanded && (
            <div className="mt-3 grid gap-3 text-sm">
              <p>Hold an open or paused proposal for sign-off, or create one that requires review from the start.</p>
              <div className="flex items-center gap-2">
                <select
                  aria-label="Select proposal"
                  value={selected}
                  onChange={(e) => {
                    setSelected(e.target.value);
                    setDraft("");
                    setEditing(false);
                    setError("");
                  }}
                  className="min-w-0 flex-1 rounded border bg-background p-2"
                >
                  <option value="">New proposal</option>
                  {rows.map((r) => (
                    <option key={r.id} value={r.id}>
                      #{r.id} ·{" "}
                      {r.review
                        ? r.review.approved_revision === r.review.revision
                          ? "Approved"
                          : "Awaiting review"
                        : r.state}{" "}
                      · {String(r.payload.summary ?? "").slice(0, 100)}
                    </option>
                  ))}
                </select>
                <Button size="sm" variant="outline" disabled={busy} onClick={() => setRefresh((n) => n + 1)}>
                  Refresh
                </Button>
              </div>
              {proposal && (
                <>
                  <p>
                    Proposal #{proposal.id} · Priority {proposal.priority} · {proposal.state}
                    {review && ` · Revision ${review.revision}`}
                  </p>
                  <pre className="max-h-64 overflow-auto whitespace-pre-wrap break-words rounded border p-2">
                    {JSON.stringify(proposal.payload, null, 2)}
                  </pre>
                  {review?.approved_by && (
                    <p>
                      Revision {review.approved_revision} approved by {review.approved_by} at{" "}
                      {new Date(review.approved_at).toLocaleString()}.
                    </p>
                  )}
                  {review && review.approvals.length > 0 && (
                    <details>
                      <summary className="cursor-pointer">Approval history</summary>
                      <ul className="mt-1">
                        {review.approvals.map((a) => (
                          <li key={a.revision}>
                            Revision {a.revision} · {a.by} · {new Date(a.at).toLocaleString()}
                          </li>
                        ))}
                      </ul>
                    </details>
                  )}
                  {canReview && !review && (
                    <Button size="sm" variant="outline" disabled={busy} onClick={() => void decide("require")}>
                      Require operator review
                    </Button>
                  )}
                  {canReview && review && !editing && (
                    <div className="flex flex-wrap gap-2">
                      {review.approved_revision !== review.revision && (
                        <Button size="sm" disabled={busy} onClick={() => void decide("approve")}>
                          Approve revision {review.revision}
                        </Button>
                      )}
                      <Button
                        size="sm"
                        variant="outline"
                        disabled={busy}
                        onClick={() => {
                          setDraft(String(proposal.payload.summary ?? ""));
                          setEditing(true);
                        }}
                      >
                        Edit summary
                      </Button>
                    </div>
                  )}
                </>
              )}
              {(!selected || editing) && (
                <>
                  <Textarea
                    aria-label="Proposed work"
                    value={draft}
                    maxLength={8000}
                    onChange={(e) => setDraft(e.target.value)}
                    placeholder="Describe the exact work for approval"
                  />
                  <Button
                    size="sm"
                    disabled={busy || !draft.trim()}
                    onClick={() => void decide(editing ? "edit" : "create")}
                  >
                    {editing ? "Save for review" : "Create pending proposal"}
                  </Button>
                </>
              )}
              {error && (
                <p role="alert" className="text-destructive">
                  {error}
                </p>
              )}
            </div>
          )}
        </details>
      </CardContent>
    </Card>
  );
}
