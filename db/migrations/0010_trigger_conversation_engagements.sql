-- Retain the source engagement after a trigger conversation finishes or its task
-- is archived/deleted. Losing the task must not turn a scoped history into a global one.
CREATE TABLE IF NOT EXISTS conversation_engagements (
    conversation_id BIGINT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    task_id BIGINT NOT NULL,
    authorized_until TIMESTAMPTZ,
    PRIMARY KEY (conversation_id, task_id)
);
