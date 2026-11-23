-- Explicit OpenAI Responses API access_programs.cyber selection. Empty keeps
-- the historical provider default and does not claim project approval.
ALTER TABLE llm_profiles ADD COLUMN IF NOT EXISTS cyber_access_program TEXT NOT NULL DEFAULT '';
ALTER TABLE llm_profiles ADD CONSTRAINT llm_profiles_cyber_access_program_check
    CHECK (cyber_access_program = '' OR (
        format = 'openai-responses' AND
        cyber_access_program IN ('standard', 'daybreak_blue', 'daybreak_red')
    ));
