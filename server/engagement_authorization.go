package server

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/Cyzerx/Artex-Moses-mode/db"
	"github.com/Cyzerx/Artex-Moses-mode/guard"
	"github.com/Cyzerx/Artex-Moses-mode/llmrec"
)

var errEngagementExpired = errors.New("the engagement authorization has expired")

func (t *Task) authorizationExpired(now time.Time) bool {
	return t != nil && t.AuthorizedUntil != nil && !now.Before(*t.AuthorizedUntil)
}

// authorizedContext applies a real cancellation deadline to external work,
// including detached runs. Graceful task settlement uses a separate time budget.
func (t *Task) authorizedContext(parent context.Context) (context.Context, context.CancelFunc) {
	if t == nil || t.AuthorizedUntil == nil {
		return parent, func() {}
	}
	parent = guard.WithEngagementEnd(parent, *t.AuthorizedUntil)
	return context.WithDeadlineCause(parent, *t.AuthorizedUntil, errEngagementExpired)
}

// The watcher seals the local lifecycle, even if a task is parked in the queue,
// paused, or reloaded after expiry. Execution contexts enforce the actual cutoff.
func (e *Engine) startAuthorizationWatch(parent context.Context, t *Task) {
	if t == nil || t.AuthorizedUntil == nil {
		return
	}
	e.deleteMu.RLock()
	if e.IsDeleting(t.ID) {
		e.deleteMu.RUnlock()
		return
	}
	if _, exists := e.authWatch.LoadOrStore(t.ID, true); exists {
		e.deleteMu.RUnlock()
		return
	}
	rt := e.registerTaskRoutines(parent, t.ID, 1)
	e.deleteMu.RUnlock()
	if rt == nil {
		e.authWatch.Delete(t.ID)
		return
	}
	runTaskRoutine(rt, func(ctx context.Context) {
		deadline, stop := t.authorizedContext(ctx)
		defer stop()
		<-deadline.Done()
		if ctx.Err() != nil || e.IsDeleting(t.ID) {
			return
		}
		e.cancelExec(t.ID, errEngagementExpired)
		for {
			won, err := e.m.SetTaskStatusGuarded(t.ID, "timeout", "authorization_expired")
			if err == nil {
				if won {
					e.emitActivity(t, db.Activity{Worker: "system", Kind: "text", Summary: "Engagement authorization expired. External work stopped."})
				}
				return
			}
			log.Printf("[authorization] task %s: %v", t.ID, err)
			if sleepCtx(ctx, deadlinePollInterval) {
				return
			}
		}
	})
}

func (s *Server) conversationAuthorization(ctx context.Context, id int64) (context.Context, context.CancelFunc, error) {
	// Read ownership under a bounded local-only context. A user stop must still
	// let the caller seal the original retest with its stopped disposition.
	lookup, done := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer done()
	ids, end, err := s.m.pg.ConversationEngagements(lookup, id)
	if err != nil {
		return ctx, func() {}, err
	}
	if len(ids) > 0 {
		ctx = llmrec.WithPrivateTaskIDs(ctx, ids)
	}
	ctx, stop := (&Task{AuthorizedUntil: end}).authorizedContext(ctx)
	return ctx, stop, nil
}
