ALTER TABLE qag.runs ADD COLUMN history_policy text NOT NULL DEFAULT 'auto'
 CHECK(history_policy IN ('auto','retain','unretained'));
ALTER TABLE qag.run_batches ADD COLUMN step_id uuid;
ALTER TABLE qag.run_batches ADD CONSTRAINT batch_step_owner
 FOREIGN KEY(run_id,step_id) REFERENCES qag.run_steps(run_id,step_id);
