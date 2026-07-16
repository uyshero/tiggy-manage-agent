CREATE UNIQUE INDEX IF NOT EXISTS session_interventions_one_pending_plan_approval_per_turn
    ON session_interventions (session_id, turn_id)
    WHERE kind = 'plan_approval' AND status = 'pending';
