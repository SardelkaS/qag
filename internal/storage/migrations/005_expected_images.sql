CREATE TABLE qag.expected_images (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    test_case_id uuid NOT NULL REFERENCES qag.test_cases(id),
    filename text NOT NULL,
    content_type text NOT NULL CHECK (content_type IN ('image/png','image/jpeg','image/webp')),
    size_bytes integer NOT NULL CHECK (size_bytes BETWEEN 1 AND 10485760),
    width integer NOT NULL CHECK (width > 0),
    height integer NOT NULL CHECK (height > 0),
    sha256 text NOT NULL CHECK (length(sha256)=64),
    data bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (octet_length(data)=size_bytes),
    CHECK (width::bigint*height::bigint <= 20000000)
);
CREATE INDEX expected_images_case ON qag.expected_images(test_case_id);
