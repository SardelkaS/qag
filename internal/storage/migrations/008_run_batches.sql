CREATE TABLE qag.run_batches (
 id uuid PRIMARY KEY,
 run_id uuid NOT NULL REFERENCES qag.runs(id) ON DELETE CASCADE,
 idempotency_key uuid NOT NULL UNIQUE,
 request_hash text NOT NULL,
 state text NOT NULL CHECK(state IN ('running','completed','cancelled','interrupted')),
 started_at timestamptz NOT NULL DEFAULT now(),
 finished_at timestamptz,
 CHECK((state='running')=(finished_at IS NULL)),
 UNIQUE(id,run_id)
);
CREATE UNIQUE INDEX one_active_batch ON qag.run_batches(run_id) WHERE state='running';
CREATE TABLE qag.run_batch_items (
 batch_id uuid NOT NULL,
 run_id uuid NOT NULL,
 position integer NOT NULL CHECK(position>0),
 block_id uuid NOT NULL,
 state text NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','running','completed','cancelled','interrupted')),
 attempt_id uuid REFERENCES qag.call_attempts(id) DEFERRABLE INITIALLY DEFERRED,
 PRIMARY KEY(batch_id,position),
 FOREIGN KEY(batch_id,run_id) REFERENCES qag.run_batches(id,run_id) ON DELETE CASCADE,
 UNIQUE(batch_id,position,run_id,block_id),
 CHECK(state <> 'pending' OR attempt_id IS NULL)
);
ALTER TABLE qag.call_attempts ADD COLUMN batch_id uuid, ADD COLUMN batch_position integer;
ALTER TABLE qag.call_attempts ADD CONSTRAINT attempt_batch_pair CHECK((batch_id IS NULL)=(batch_position IS NULL));
ALTER TABLE qag.call_attempts ADD CONSTRAINT attempt_batch_original CHECK(batch_id IS NULL OR (variant_id IS NULL AND run_id IS NOT NULL));
ALTER TABLE qag.call_attempts ADD CONSTRAINT attempt_batch_owner FOREIGN KEY(batch_id,batch_position,run_id,block_id)
 REFERENCES qag.run_batch_items(batch_id,position,run_id,block_id) DEFERRABLE INITIALLY DEFERRED;
CREATE UNIQUE INDEX one_attempt_per_batch_item ON qag.call_attempts(batch_id,batch_position) WHERE batch_id IS NOT NULL;
