-- +goose Up
ALTER TABLE smtp
    ADD COLUMN password_ciphertext VARCHAR(2048) NOT NULL DEFAULT '';

-- +goose Down
-- Ciphertext must be explicitly rolled back to the legacy password column first.
DROP TEMPORARY TABLE IF EXISTS smtp_ciphertext_must_be_rolled_back_before_schema_down;
CREATE TEMPORARY TABLE smtp_ciphertext_must_be_rolled_back_before_schema_down (
    guard INTEGER PRIMARY KEY
);
INSERT INTO smtp_ciphertext_must_be_rolled_back_before_schema_down (guard) VALUES (1);
INSERT INTO smtp_ciphertext_must_be_rolled_back_before_schema_down (guard)
SELECT 1
FROM smtp
WHERE password_ciphertext <> ''
LIMIT 1;
DROP TEMPORARY TABLE smtp_ciphertext_must_be_rolled_back_before_schema_down;

ALTER TABLE smtp
    DROP COLUMN password_ciphertext;
