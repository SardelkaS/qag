CREATE TABLE qag.run_call_variants (
 id uuid PRIMARY KEY,
 run_id uuid NOT NULL REFERENCES qag.runs(id) ON DELETE CASCADE,
 block_id uuid NOT NULL,
 name text NOT NULL CHECK(char_length(name)<=200),
 position integer NOT NULL CHECK(position BETWEEN 1 AND 20),
 revision bigint NOT NULL CHECK(revision>0),
 current_version_id uuid NOT NULL,
 UNIQUE(run_id,block_id,position),
 UNIQUE(id,run_id,block_id)
);
CREATE TABLE qag.run_call_variant_versions (
 id uuid PRIMARY KEY,
 variant_id uuid NOT NULL,
 run_id uuid NOT NULL,
 block_id uuid NOT NULL,
 revision bigint NOT NULL CHECK(revision>0),
 request jsonb NOT NULL CHECK(jsonb_typeof(request)='object'),
 created_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY(variant_id,run_id,block_id) REFERENCES qag.run_call_variants(id,run_id,block_id) ON DELETE CASCADE,
 UNIQUE(variant_id,revision),
 UNIQUE(id,variant_id,run_id,block_id)
);
ALTER TABLE qag.run_call_variants ADD CONSTRAINT variant_current_version
 FOREIGN KEY(current_version_id,id,run_id,block_id)
 REFERENCES qag.run_call_variant_versions(id,variant_id,run_id,block_id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE qag.call_attempts ADD COLUMN variant_id uuid, ADD COLUMN variant_version_id uuid;
ALTER TABLE qag.call_attempts ADD CONSTRAINT attempt_variant_pair CHECK ((variant_id IS NULL)=(variant_version_id IS NULL));
ALTER TABLE qag.call_attempts ADD CONSTRAINT attempt_variant_owner FOREIGN KEY(variant_version_id,variant_id,run_id,block_id)
 REFERENCES qag.run_call_variant_versions(id,variant_id,run_id,block_id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE qag.call_attempts ADD CONSTRAINT attempt_variant_run CHECK(variant_id IS NULL OR run_id IS NOT NULL);
ALTER TABLE qag.call_attempts DROP CONSTRAINT call_attempts_run_id_test_case_id_block_id_attempt_number_key;
ALTER TABLE qag.call_attempts ADD CONSTRAINT attempt_number_per_tab UNIQUE NULLS NOT DISTINCT(run_id,test_case_id,block_id,variant_id,attempt_number);
