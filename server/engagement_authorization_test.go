package server

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/Cyzerx/Moses-x1/db"
)

func TestEngagementDeadlineCancelsInflight(t *testing.T) {
	end := time.Now().Add(50 * time.Millisecond)
	task := Task{AuthorizedUntil: &end}
	ctx, stop := task.authorizedContext(context.Background())
	defer stop()
	select {
	case <-ctx.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("in-flight work not canceled")
	}
	if !errors.Is(context.Cause(ctx), errEngagementExpired) {
		t.Fatal(context.Cause(ctx))
	}
	if task.authorizationExpired(end.Add(-time.Nanosecond)) || !task.authorizationExpired(end) {
		t.Fatal("expiry comparison did not honor boundary")
	}
	after, release := task.authorizedContext(context.Background())
	defer release()
	if after.Err() == nil {
		t.Fatal("new operation started after expiry")
	}
}

func TestEngagementDeadlineBlocksQueuedAndRestoredTask(t *testing.T) {
	s, _, _ := trafficEvidenceServer(t)
	end := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Minute)
	task, err := s.m.CreateTaskWithOptions("expired fixture", "review locally", db.TaskCreateOptions{AuthorizedUntil: &end, TimeoutSeconds: 3600})
	if err != nil {
		t.Fatal(err)
	}
	taskID, _ := strconv.ParseInt(task.ID, 10, 64)
	t.Cleanup(func() { s.m.pg.Exec("DELETE FROM tasks WHERE id=$1", taskID) })
	if _, err = s.m.pg.Exec(`UPDATE tasks SET paused=true,paused_at=now()-interval '1 day',queued=true,queued_at=now(),status='running' WHERE id=$1`, taskID); err != nil {
		t.Fatal(err)
	}
	record, err := s.m.pg.GetTask(taskID)
	if err != nil || record.AuthorizedUntil == nil || !record.AuthorizedUntil.Equal(end) {
		t.Fatal("authorization lost on reload", err)
	}
	if err = s.m.ApplyTaskAdmission(task.ID, "running", "running", false, "resume", false); err == nil {
		t.Fatal("database admitted expired task")
	}
	if _, err = s.admitTask(task, "resume"); !errors.Is(err, errEngagementExpired) {
		t.Fatalf("resume: %v", err)
	}
	// The fixture server's root is canceled. Drive the independent watcher under
	// a fresh local context with no model, workers or network targets.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.engine.startAuthorizationWatch(ctx, task)
	limit := time.Now().Add(5 * time.Second)
	for time.Now().Before(limit) {
		var status, reason string
		var queued bool
		err = s.m.pg.QueryRow("SELECT status,end_reason,queued FROM tasks WHERE id=$1", taskID).Scan(&status, &reason, &queued)
		if err != nil {
			t.Fatal(err)
		}
		if status == "timeout" && reason == "authorization_expired" && !queued {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("expired parked task was not locally sealed")
}

func TestTriggerConversationKeepsEarliestEngagementEnd(t *testing.T) {
	s, _, _ := trafficEvidenceServer(t)
	early := time.Now().UTC().Truncate(time.Microsecond).Add(time.Hour)
	late := early.Add(time.Hour)
	ids := []int64{}
	for _, end := range []time.Time{late, early} {
		task, err := s.m.CreateTaskWithOptions("local trigger fixture", "review", db.TaskCreateOptions{AuthorizedUntil: &end})
		if err != nil {
			t.Fatal(err)
		}
		id, _ := strconv.ParseInt(task.ID, 10, 64)
		t.Cleanup(func() { s.m.pg.Exec("DELETE FROM tasks WHERE id=$1", id) })
		ids = append(ids, id)
	}
	conv, err := s.m.pg.CreateConversationGated("mainagent", "synthetic merged run", nil, false, ids...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.m.pg.Exec("DELETE FROM conversations WHERE id=$1", conv.ID) })
	owners, end, err := s.m.pg.ConversationEngagements(context.Background(), conv.ID)
	if err != nil || len(owners) != 2 || end == nil || !end.Equal(early) {
		t.Fatalf("owners=%v end=%v err=%v", owners, end, err)
	}
	ctx, release, err := s.conversationAuthorization(context.Background(), conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if bound, ok := ctx.Deadline(); !ok || !bound.Equal(early) {
		t.Fatal("continued chat loses earliest end")
	}
	if _, err := s.m.pg.Exec("UPDATE tasks SET deleted_at=now() WHERE id=$1", ids[0]); err != nil {
		t.Fatal(err)
	}
	_, stop, err := s.conversationAuthorization(context.Background(), conv.ID)
	defer stop()
	if err == nil {
		t.Fatal("a missing source must not make a conversation unrestricted")
	}
}
