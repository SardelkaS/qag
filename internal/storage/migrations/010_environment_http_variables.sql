ALTER TABLE qag.environment_versions
    ADD COLUMN http_variables jsonb NOT NULL DEFAULT '{}'::jsonb
    CHECK (jsonb_typeof(http_variables) = 'object');
