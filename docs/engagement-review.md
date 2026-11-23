# Engagement privacy and review

## Capture and credentials

Start a browser recording for the intended engagement, approved origins, and Artex-Moses-mode recipient.
Queued exchanges retain that recording's destination. Changing the recipient requires a fresh
credential. Captures from the shared proxy and legacy imports can lack reliable ownership.
Those captures start in the operator inventory. An agent can search, read or bind a capture only
when its ownership matches the agent's engagement. A hostname can belong to several clients.
In **Traffic**, review the exact exchanges and assign the selected records to their engagement.
The assignment records the operator and time. An exchange with an owner cannot be reassigned.
Historical findings retain their existing evidence associations.

The planner and worker receive account references in routine context. Passwords and operator notes
belong in the task credential manager. Select a provider authorized for the engagement and explicitly
provide its credential when changing a saved endpoint. Review and rotate any secrets you know were
sent before upgrading. A local software change cannot withdraw a previous provider request.

Windows capture storage requires a filesystem that supports Windows access-control lists. On
startup Artex-Moses-mode restricts the capture tree, including existing files, to the Windows account
running the service. It also restricts each new staged body before writing evidence bytes.
If an upgrade reports a reparse point or a multiply linked file, move the captured data into
private regular files inside the capture directory. If an older installation used a different
service account, provision ownership for the new account before retrying startup.

## Engagement controls

Set a fixed authorization end when creating a time-limited engagement. The operating duration can
pause. The authorization end stays tied to the recorded instant. Use proposal review when a
particular piece of work requires separate operator sign-off.

## Reports and evidence

The task report distinguishes recorded findings, confirmed dispositions and machine verification.
A finding can appear in both of the latter counts. Asset totals include assets associated with
the current engagement. Use the integrity check in the task's Report tab to verify the graph,
binding metadata and evidence files. Choose database-only verification explicitly for a restore
that intentionally omitted files. The result identifies skipped checks.

In Findings, select the records for the offline review. Inspect the preview before downloading.
The client receives the selected status and evidence references. Reviewed titles are optional.
Keep full archives and raw traffic within your evidence-handling process.

## Remediation notes

Open **Functions → Remediation Notes** to search the generic reviewer references by text or tag.
The owner, source and review date appear with each note. Retired notes require an explicit
selection. To publish a curated replacement, set `ARTEX_MOSES_MODE_REMEDIATION_NOTES` to a read-only JSON
file on the server, following [the bundled catalogue](../server/remediation_notes.json).
For Compose, mount that file read-only and set the variable to its container path. Publishing a
catalogue marked `all-reviewers` makes its contents visible to authenticated reviewers. The
catalogue does not import client findings or feed notes into model prompts.
