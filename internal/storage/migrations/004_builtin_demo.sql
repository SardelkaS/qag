CREATE TABLE qag.builtin_demo (
    key text PRIMARY KEY CHECK (key='local-demo'),
    folder_id uuid NOT NULL UNIQUE REFERENCES qag.folders(id),
    test_case_id uuid NOT NULL UNIQUE REFERENCES qag.test_cases(id),
    environment_id uuid NOT NULL UNIQUE REFERENCES qag.environments(id),
    http_resource_id uuid NOT NULL UNIQUE REFERENCES qag.resources(id),
    grpc_resource_id uuid NOT NULL UNIQUE REFERENCES qag.resources(id),
    sql_resource_id uuid NOT NULL UNIQUE REFERENCES qag.resources(id),
    seed_version integer NOT NULL CHECK(seed_version>0)
);
