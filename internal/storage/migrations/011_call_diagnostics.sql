CREATE TABLE qag.http_variable_probes (
    id uuid PRIMARY KEY,
    environment_id uuid NOT NULL REFERENCES qag.environments(id) DEFERRABLE INITIALLY DEFERRED,
    resource_id uuid REFERENCES qag.resources(id) DEFERRABLE INITIALLY DEFERRED,
    idempotency_key uuid NOT NULL UNIQUE,
    request_hash text NOT NULL,
    state text NOT NULL CHECK (state IN ('queued','running','completed','error','timed_out','cancelled','interrupted')),
    started_at timestamptz NOT NULL,
    finished_at timestamptz,
    payload jsonb NOT NULL CHECK (jsonb_typeof(payload)='object')
);

CREATE TABLE qag.call_logs (
    id uuid PRIMARY KEY,
    attempt_id uuid REFERENCES qag.call_attempts(id) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED,
    probe_id uuid REFERENCES qag.http_variable_probes(id) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED,
    run_id uuid REFERENCES qag.runs(id) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED,
    test_case_id uuid REFERENCES qag.test_cases(id) DEFERRABLE INITIALLY DEFERRED,
    environment_id uuid NOT NULL REFERENCES qag.environments(id) DEFERRABLE INITIALLY DEFERRED,
    resource_id uuid REFERENCES qag.resources(id) DEFERRABLE INITIALLY DEFERRED,
    protocol text NOT NULL CHECK (protocol IN ('http','grpc','postgresql')),
    origin text NOT NULL CHECK (origin IN ('editor','run','variant','variable_probe')),
    stage text NOT NULL CHECK (stage IN ('main','header_source','environment_variable_source','resource_variable_source','variable_probe')),
    state text NOT NULL CHECK (state IN ('queued','running','completed','error','timed_out','cancelled','interrupted')),
    started_at timestamptz NOT NULL,
    summary jsonb NOT NULL CHECK (jsonb_typeof(summary)='object'),
    payload jsonb NOT NULL CHECK (jsonb_typeof(payload)='object'),
    CHECK ((attempt_id IS NULL) <> (probe_id IS NULL))
);
CREATE INDEX call_logs_time_idx ON qag.call_logs(started_at DESC,id DESC);
CREATE INDEX call_logs_attempt_idx ON qag.call_logs(attempt_id);
CREATE INDEX call_logs_probe_idx ON qag.call_logs(probe_id);
CREATE INDEX call_logs_run_idx ON qag.call_logs(run_id,started_at DESC,id DESC);
CREATE INDEX call_logs_resource_idx ON qag.call_logs(environment_id,resource_id,started_at DESC,id DESC);
CREATE INDEX call_logs_stage_idx ON qag.call_logs(protocol,stage,state,started_at DESC,id DESC);
