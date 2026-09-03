-- +goose Up
ALTER TABLE users
    MODIFY COLUMN api_key VARCHAR(255) NULL,
    ADD COLUMN api_key_verifier BINARY(32) NULL,
    ADD COLUMN api_key_verifier_key_id VARCHAR(64) NULL,
    ADD UNIQUE INDEX users_api_key_verifier_unique
        (api_key_verifier_key_id, api_key_verifier);

-- +goose Down
-- Refuse before mutation whenever rollback would discard irreversible state.
DROP TEMPORARY TABLE IF EXISTS api_key_verifier_must_restore_backup_before_down;
CREATE TEMPORARY TABLE api_key_verifier_must_restore_backup_before_down (
    guard INTEGER PRIMARY KEY
);
INSERT INTO api_key_verifier_must_restore_backup_before_down (guard) VALUES (1);
INSERT INTO api_key_verifier_must_restore_backup_before_down (guard)
SELECT 1
FROM users
WHERE api_key IS NULL
   OR api_key = ''
   OR api_key_verifier IS NOT NULL
   OR api_key_verifier_key_id IS NOT NULL
LIMIT 1;
DROP TEMPORARY TABLE api_key_verifier_must_restore_backup_before_down;

ALTER TABLE users
    DROP INDEX users_api_key_verifier_unique,
    DROP COLUMN api_key_verifier,
    DROP COLUMN api_key_verifier_key_id,
    MODIFY COLUMN api_key VARCHAR(255) NOT NULL;
