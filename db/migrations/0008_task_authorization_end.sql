-- A fixed client authorization boundary is independent of the pausable runtime budget.
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS authorized_until TIMESTAMPTZ;
