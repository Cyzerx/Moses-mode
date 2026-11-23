-- Existing installations used an opt-in target gate. Turn it on for stored
-- tasks and conversations once during upgrade. Operators may explicitly turn
-- it off again through the API after the upgrade if their workflow needs it.
UPDATE tasks SET target_gate = true WHERE target_gate = false;
UPDATE conversations SET target_gate = true WHERE target_gate = false;
