-- NULL denotes an ordinary proposal, including nodes restored from old archives.
ALTER TABLE exploration_nodes ADD COLUMN IF NOT EXISTS proposal_review JSONB;

CREATE OR REPLACE FUNCTION enforce_proposal_review() RETURNS trigger AS $$
BEGIN
    IF NEW.kind <> 'intent' OR OLD.proposal_review IS NULL THEN RETURN NEW; END IF;
    IF NEW.proposal_review IS NULL THEN
        RAISE EXCEPTION 'a selected proposal must retain its review history';
    END IF;
    IF NEW.payload IS DISTINCT FROM OLD.payload OR NEW.priority IS DISTINCT FROM OLD.priority THEN
        IF OLD.state = 'running' THEN
            RAISE EXCEPTION 'pause the approved proposal before editing its scope';
        END IF;
        NEW.proposal_review := (OLD.proposal_review - 'approved_by' - 'approved_at' - 'approved_revision') ||
            jsonb_build_object('revision', (OLD.proposal_review->>'revision')::bigint + 1);
    END IF;
    IF NEW.state IS DISTINCT FROM OLD.state AND NEW.state IN ('running','done','exhausted') AND
       (NEW.proposal_review->>'approved_revision') IS DISTINCT FROM (NEW.proposal_review->>'revision') THEN
        RAISE EXCEPTION 'proposal requires operator approval of its current revision';
    END IF;
    RETURN NEW;
END
$$ LANGUAGE plpgsql;
DROP TRIGGER IF EXISTS trg_enforce_proposal_review ON exploration_nodes;
CREATE TRIGGER trg_enforce_proposal_review BEFORE UPDATE ON exploration_nodes
FOR EACH ROW EXECUTE FUNCTION enforce_proposal_review();
