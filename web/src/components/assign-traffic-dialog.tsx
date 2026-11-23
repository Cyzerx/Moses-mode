"use client";

import { useEffect, useState } from "react";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { api, type CaptureAssignment } from "@/lib/api";
import type { Task } from "@/lib/types";

export function AssignTrafficDialog({
  ids,
  onClose,
  onAssigned,
}: {
  ids: string[];
  onClose: () => void;
  onAssigned: () => void;
}) {
  const [tasks, setTasks] = useState<Task[]>([]);
  const [rows, setRows] = useState<CaptureAssignment[]>([]);
  const [task, setTask] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    let active = true;
    Promise.all([api.trafficAssignments(ids), api.tasks()])
      .then(([records, list]) => {
        if (active) {
          setRows(records);
          setTasks(list.tasks);
        }
      })
      .catch((e: Error) => {
        if (active) setError(e.message);
      });
    return () => {
      active = false;
    };
  }, [ids]);

  async function assign() {
    setBusy(true);
    try {
      await api.trafficAssignments(ids, task);
      onAssigned();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not assign captures");
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !busy) onClose();
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Assign selected captures</DialogTitle>
          <DialogDescription>
            The engagement’s authorized model tools and evidence exports will be able to read these exchanges, including
            their headers, bodies and stream messages. An assignment is permanent.
          </DialogDescription>
        </DialogHeader>
        <div className="max-h-60 space-y-2 overflow-auto">
          {rows.map((row) => (
            <div key={row.id} className="break-all text-sm">
              <strong>{row.method}</strong> {row.url}
              <div className="text-muted-foreground text-xs">
                {row.id} · {row.task_id ? `Engagement #${row.task_id}` : "Unassigned"}
                {row.assigned_by && row.assigned_at
                  ? ` · Assigned by ${row.assigned_by} on ${new Date(row.assigned_at * 1000).toLocaleString()}`
                  : ""}
              </div>
            </div>
          ))}
        </div>
        <label className="grid gap-1 text-sm">
          Receiving engagement
          <select className="rounded border bg-background p-2" value={task} onChange={(e) => setTask(e.target.value)}>
            <option value="">Choose an engagement</option>
            {tasks.map((item) => (
              <option key={item.id} value={item.id}>
                #{item.id} · {item.description}
              </option>
            ))}
          </select>
        </label>
        {error && (
          <p className="text-destructive text-sm" role="alert">
            {error}
          </p>
        )}
        <DialogFooter>
          <Button variant="outline" disabled={busy} onClick={onClose}>
            Cancel
          </Button>
          <Button
            disabled={busy || !task || rows.length !== ids.length || rows.some((row) => row.task_id)}
            onClick={() => void assign()}
          >
            Assign {ids.length} to engagement #{task || "…"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
