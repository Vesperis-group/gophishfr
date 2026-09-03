-- +goose Up
ALTER TABLE webhooks
    ADD COLUMN secret_ciphertext VARCHAR(2048) NOT NULL DEFAULT '';

-- +goose Down
-- Ciphertext must be explicitly rolled back to the legacy secret column first.
DROP TEMPORARY TABLE IF EXISTS webhooks_ciphertext_must_be_rolled_back_before_schema_down;
CREATE TEMPORARY TABLE webhooks_ciphertext_must_be_rolled_back_before_schema_down (
    guard INTEGER PRIMARY KEY
);
INSERT INTO webhooks_ciphertext_must_be_rolled_back_before_schema_down (guard) VALUES (1);
INSERT INTO webhooks_ciphertext_must_be_rolled_back_before_schema_down (guard)
SELECT 1
FROM webhooks
WHERE secret_ciphertext <> ''
LIMIT 1;
DROP TEMPORARY TABLE webhooks_ciphertext_must_be_rolled_back_before_schema_down;

ALTER TABLE webhooks
    DROP COLUMN secret_ciphertext;
