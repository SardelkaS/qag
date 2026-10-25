CREATE TABLE qag.folders (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 200),
    normalized_name text NOT NULL UNIQUE,
    archived_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE qag.test_cases (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    folder_id uuid NOT NULL REFERENCES qag.folders(id),
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 200),
    normalized_name text NOT NULL,
    archived_at timestamptz,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    steps jsonb NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(steps) = 'array'),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (folder_id, normalized_name)
);

CREATE TABLE qag.environments (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 200),
    normalized_name text NOT NULL UNIQUE,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    current_version_id uuid,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE qag.environment_versions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    environment_id uuid NOT NULL REFERENCES qag.environments(id),
    revision bigint NOT NULL CHECK (revision > 0),
    variables jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(variables) = 'object'),
    encrypted_secrets bytea NOT NULL,
    secret_names jsonb NOT NULL DEFAULT '[]',
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (environment_id, revision),
    UNIQUE (environment_id, id)
);

ALTER TABLE qag.environments ADD CONSTRAINT environment_current_version
    FOREIGN KEY (id, current_version_id) REFERENCES qag.environment_versions(environment_id, id)
    DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE qag.resources (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    environment_id uuid NOT NULL REFERENCES qag.environments(id),
    name text NOT NULL CHECK (name ~ '^[A-Z][A-Z0-9_]*$'),
    kind text NOT NULL CHECK (kind IN ('http', 'grpc', 'postgresql')),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    current_version_id uuid,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (environment_id, name)
);

CREATE TABLE qag.resource_versions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    resource_id uuid NOT NULL REFERENCES qag.resources(id),
    revision bigint NOT NULL CHECK (revision > 0),
    config jsonb NOT NULL CHECK (jsonb_typeof(config) = 'object'),
    encrypted_secrets bytea NOT NULL,
    secret_names jsonb NOT NULL DEFAULT '[]',
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (resource_id, revision),
    UNIQUE (resource_id, id)
);

ALTER TABLE qag.resources ADD CONSTRAINT resource_current_version
    FOREIGN KEY (id, current_version_id) REFERENCES qag.resource_versions(resource_id, id)
    DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE qag.runs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    test_case_id uuid NOT NULL REFERENCES qag.test_cases(id),
    environment_version_id uuid NOT NULL REFERENCES qag.environment_versions(id),
    snapshot jsonb NOT NULL CHECK (jsonb_typeof(snapshot) = 'object'),
    context jsonb NOT NULL CHECK (jsonb_typeof(context) = 'object'),
    status text NOT NULL DEFAULT 'running'
        CHECK (status IN ('running', 'passed', 'failed', 'blocked', 'cancelled', 'interrupted')),
    retention_reasons text[] NOT NULL DEFAULT '{}'
        CHECK (retention_reasons <@ ARRAY['first_run', 'first_success', 'manual']::text[]),
    idempotency_key uuid NOT NULL UNIQUE,
    request_hash text NOT NULL,
    started_at timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz,
    CHECK ((status = 'running') = (finished_at IS NULL)),
    CHECK (NOT ('first_success' = ANY(retention_reasons)) OR status = 'passed'),
    CHECK (status <> 'running' OR cardinality(retention_reasons) = 0)
);

CREATE UNIQUE INDEX one_running_case ON qag.runs(test_case_id) WHERE status = 'running';
CREATE UNIQUE INDEX one_first_run ON qag.runs(test_case_id) WHERE 'first_run' = ANY(retention_reasons);
CREATE UNIQUE INDEX one_first_success ON qag.runs(test_case_id) WHERE 'first_success' = ANY(retention_reasons);
CREATE INDEX run_history ON qag.runs(test_case_id, finished_at DESC, id DESC);

CREATE TABLE qag.run_resource_versions (
    run_id uuid NOT NULL REFERENCES qag.runs(id) ON DELETE CASCADE,
    resource_name text NOT NULL,
    resource_version_id uuid NOT NULL REFERENCES qag.resource_versions(id),
    PRIMARY KEY (run_id, resource_name)
);

CREATE TABLE qag.run_steps (
    run_id uuid NOT NULL REFERENCES qag.runs(id) ON DELETE CASCADE,
    step_id uuid NOT NULL,
    status text NOT NULL DEFAULT 'not_run'
        CHECK (status IN ('not_run', 'passed', 'failed', 'blocked', 'skipped')),
    comment text NOT NULL DEFAULT '',
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    PRIMARY KEY (run_id, step_id)
);

CREATE TABLE qag.call_attempts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id uuid REFERENCES qag.runs(id) ON DELETE CASCADE,
    test_case_id uuid NOT NULL REFERENCES qag.test_cases(id),
    block_id uuid NOT NULL,
    case_revision bigint NOT NULL CHECK (case_revision > 0),
    environment_version_id uuid NOT NULL REFERENCES qag.environment_versions(id),
    resource_version_id uuid NOT NULL REFERENCES qag.resource_versions(id),
    attempt_number integer NOT NULL CHECK (attempt_number > 0),
    idempotency_key uuid NOT NULL UNIQUE,
    request_hash text NOT NULL,
    state text NOT NULL DEFAULT 'queued'
        CHECK (state IN ('queued', 'running', 'completed', 'error', 'timed_out', 'cancelled', 'interrupted')),
    safe_request jsonb NOT NULL DEFAULT '{}',
    result jsonb,
    started_at timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz,
    duration_ms bigint NOT NULL DEFAULT 0 CHECK (duration_ms >= 0),
    CHECK ((state IN ('queued', 'running')) = (finished_at IS NULL)),
    UNIQUE NULLS NOT DISTINCT (run_id, test_case_id, block_id, attempt_number)
);

CREATE UNIQUE INDEX one_executing_block_per_run ON qag.call_attempts(run_id)
    WHERE run_id IS NOT NULL AND state IN ('queued', 'running');
