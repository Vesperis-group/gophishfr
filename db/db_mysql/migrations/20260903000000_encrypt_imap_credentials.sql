-- +goose Up
-- Reject ambiguous legacy data before enforcing the one-row-per-user invariant.
DROP TEMPORARY TABLE IF EXISTS imap_duplicate_user_id_rows_must_be_resolved;
CREATE TEMPORARY TABLE imap_duplicate_user_id_rows_must_be_resolved (
    guard INTEGER PRIMARY KEY
);
INSERT INTO imap_duplicate_user_id_rows_must_be_resolved (guard) VALUES (1);
INSERT INTO imap_duplicate_user_id_rows_must_be_resolved (guard)
SELECT 1
FROM imap
GROUP BY user_id
HAVING COUNT(*) > 1;
DROP TEMPORARY TABLE imap_duplicate_user_id_rows_must_be_resolved;

-- A single ALTER keeps MySQL's non-transactional DDL change atomic.
ALTER TABLE imap
    ADD COLUMN password_ciphertext VARCHAR(2048) NOT NULL DEFAULT '',
    ADD UNIQUE INDEX imap_user_id_unique (user_id);

-- +goose Down
-- Ciphertext must be explicitly rolled back to the legacy password column first.
DROP TEMPORARY TABLE IF EXISTS imap_ciphertext_must_be_rolled_back_before_schema_down;
CREATE TEMPORARY TABLE imap_ciphertext_must_be_rolled_back_before_schema_down (
    guard INTEGER PRIMARY KEY
);
INSERT INTO imap_ciphertext_must_be_rolled_back_before_schema_down (guard) VALUES (1);
INSERT INTO imap_ciphertext_must_be_rolled_back_before_schema_down (guard)
SELECT 1
FROM imap
WHERE password_ciphertext <> ''
LIMIT 1;
DROP TEMPORARY TABLE imap_ciphertext_must_be_rolled_back_before_schema_down;

ALTER TABLE imap
    DROP INDEX imap_user_id_unique,
    DROP COLUMN password_ciphertext;
