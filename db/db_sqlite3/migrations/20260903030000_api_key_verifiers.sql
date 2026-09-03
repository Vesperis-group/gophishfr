-- +goose Up
-- SQLite cannot remove NOT NULL from api_key in place. Rebuild the complete
-- current users table while preserving its IDs, constraints, and AUTOINCREMENT
-- high-water mark.
CREATE TEMPORARY TABLE users_sequence_before_api_key_verifiers (
    high_water_mark INTEGER NOT NULL
);
INSERT INTO users_sequence_before_api_key_verifiers (high_water_mark)
SELECT CASE
    WHEN COALESCE((SELECT seq FROM sqlite_sequence WHERE name = 'users'), 0)
         > COALESCE((SELECT MAX(id) FROM users), 0)
    THEN COALESCE((SELECT seq FROM sqlite_sequence WHERE name = 'users'), 0)
    ELSE COALESCE((SELECT MAX(id) FROM users), 0)
END;

ALTER TABLE users RENAME TO users_before_api_key_verifiers;
CREATE TABLE users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    username VARCHAR(255) NOT NULL UNIQUE,
    hash VARCHAR(255),
    api_key VARCHAR(255) UNIQUE,
    role_id INTEGER,
    password_change_required BOOLEAN,
    last_login DATETIME,
    account_locked BOOLEAN,
    api_key_verifier BLOB,
    api_key_verifier_key_id VARCHAR(64)
);
INSERT INTO users (
    id, username, hash, api_key, role_id, password_change_required,
    last_login, account_locked, api_key_verifier, api_key_verifier_key_id
)
SELECT
    id, username, hash, api_key, role_id, password_change_required,
    last_login, account_locked, NULL, NULL
FROM users_before_api_key_verifiers;
DROP TABLE users_before_api_key_verifiers;
CREATE UNIQUE INDEX users_api_key_verifier_unique
ON users (api_key_verifier_key_id, api_key_verifier);

UPDATE sqlite_sequence
SET seq = (SELECT high_water_mark FROM users_sequence_before_api_key_verifiers)
WHERE name = 'users';
INSERT INTO sqlite_sequence (name, seq)
SELECT 'users', high_water_mark
FROM users_sequence_before_api_key_verifiers
WHERE NOT EXISTS (SELECT 1 FROM sqlite_sequence WHERE name = 'users');
DROP TABLE users_sequence_before_api_key_verifiers;

-- +goose Down
-- HMAC verifiers cannot reconstruct plaintext. Deliberately fail before any
-- schema mutation unless every row is still a coherent legacy row.
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
DROP TABLE api_key_verifier_must_restore_backup_before_down;

CREATE TEMPORARY TABLE users_sequence_before_api_key_verifier_down (
    high_water_mark INTEGER NOT NULL
);
INSERT INTO users_sequence_before_api_key_verifier_down (high_water_mark)
SELECT CASE
    WHEN COALESCE((SELECT seq FROM sqlite_sequence WHERE name = 'users'), 0)
         > COALESCE((SELECT MAX(id) FROM users), 0)
    THEN COALESCE((SELECT seq FROM sqlite_sequence WHERE name = 'users'), 0)
    ELSE COALESCE((SELECT MAX(id) FROM users), 0)
END;

DROP INDEX users_api_key_verifier_unique;
ALTER TABLE users RENAME TO users_with_api_key_verifiers;
CREATE TABLE users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    username VARCHAR(255) NOT NULL UNIQUE,
    hash VARCHAR(255),
    api_key VARCHAR(255) NOT NULL UNIQUE,
    role_id INTEGER,
    password_change_required BOOLEAN,
    last_login DATETIME,
    account_locked BOOLEAN
);
INSERT INTO users (
    id, username, hash, api_key, role_id, password_change_required,
    last_login, account_locked
)
SELECT
    id, username, hash, api_key, role_id, password_change_required,
    last_login, account_locked
FROM users_with_api_key_verifiers;
DROP TABLE users_with_api_key_verifiers;

UPDATE sqlite_sequence
SET seq = (SELECT high_water_mark FROM users_sequence_before_api_key_verifier_down)
WHERE name = 'users';
INSERT INTO sqlite_sequence (name, seq)
SELECT 'users', high_water_mark
FROM users_sequence_before_api_key_verifier_down
WHERE NOT EXISTS (SELECT 1 FROM sqlite_sequence WHERE name = 'users');
DROP TABLE users_sequence_before_api_key_verifier_down;
