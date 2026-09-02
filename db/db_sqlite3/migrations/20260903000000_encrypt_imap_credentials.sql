-- +goose Up
-- Reject ambiguous legacy data before enforcing the one-row-per-user invariant.
DROP TABLE IF EXISTS imap_duplicate_user_id_rows_must_be_resolved;
CREATE TEMPORARY TABLE imap_duplicate_user_id_rows_must_be_resolved (
    guard INTEGER PRIMARY KEY
);
INSERT INTO imap_duplicate_user_id_rows_must_be_resolved (guard) VALUES (1);
INSERT INTO imap_duplicate_user_id_rows_must_be_resolved (guard)
SELECT 1
FROM imap
GROUP BY user_id
HAVING COUNT(*) > 1;
DROP TABLE imap_duplicate_user_id_rows_must_be_resolved;

ALTER TABLE imap
    ADD COLUMN password_ciphertext VARCHAR(2048) NOT NULL DEFAULT '';
CREATE UNIQUE INDEX imap_user_id_unique ON imap (user_id);

-- +goose Down
-- Ciphertext must be explicitly rolled back to the legacy password column first.
DROP TABLE IF EXISTS imap_ciphertext_must_be_rolled_back_before_schema_down;
CREATE TEMPORARY TABLE imap_ciphertext_must_be_rolled_back_before_schema_down (
    guard INTEGER PRIMARY KEY
);
INSERT INTO imap_ciphertext_must_be_rolled_back_before_schema_down (guard) VALUES (1);
INSERT INTO imap_ciphertext_must_be_rolled_back_before_schema_down (guard)
SELECT 1
FROM imap
WHERE password_ciphertext <> ''
LIMIT 1;
DROP TABLE imap_ciphertext_must_be_rolled_back_before_schema_down;

DROP INDEX imap_user_id_unique;
ALTER TABLE imap RENAME TO imap_with_ciphertext;
CREATE TABLE imap (
    user_id bigint,
    host varchar(255),
    port integer,
    username varchar(255),
    password varchar(255),
    modified_date datetime default CURRENT_TIMESTAMP,
    tls BOOLEAN,
    enabled BOOLEAN,
    folder varchar(255),
    restrict_domain varchar(255),
    delete_reported_campaign_email BOOLEAN,
    last_login datetime,
    imap_freq integer,
    ignore_cert_errors BOOLEAN
);
INSERT INTO imap (
    user_id,
    host,
    port,
    username,
    password,
    modified_date,
    tls,
    enabled,
    folder,
    restrict_domain,
    delete_reported_campaign_email,
    last_login,
    imap_freq,
    ignore_cert_errors
)
SELECT
    user_id,
    host,
    port,
    username,
    password,
    modified_date,
    tls,
    enabled,
    folder,
    restrict_domain,
    delete_reported_campaign_email,
    last_login,
    imap_freq,
    ignore_cert_errors
FROM imap_with_ciphertext;
DROP TABLE imap_with_ciphertext;
