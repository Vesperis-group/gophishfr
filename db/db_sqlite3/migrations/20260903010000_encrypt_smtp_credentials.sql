-- +goose Up
ALTER TABLE smtp
    ADD COLUMN password_ciphertext VARCHAR(2048) NOT NULL DEFAULT '';

-- +goose Down
-- Ciphertext must be explicitly rolled back to the legacy password column first.
DROP TABLE IF EXISTS smtp_ciphertext_must_be_rolled_back_before_schema_down;
CREATE TEMPORARY TABLE smtp_ciphertext_must_be_rolled_back_before_schema_down (
    guard INTEGER PRIMARY KEY
);
INSERT INTO smtp_ciphertext_must_be_rolled_back_before_schema_down (guard) VALUES (1);
INSERT INTO smtp_ciphertext_must_be_rolled_back_before_schema_down (guard)
SELECT 1
FROM smtp
WHERE password_ciphertext <> ''
LIMIT 1;
DROP TABLE smtp_ciphertext_must_be_rolled_back_before_schema_down;

ALTER TABLE smtp RENAME TO smtp_with_ciphertext;
CREATE TABLE smtp (
    id integer primary key autoincrement,
    user_id bigint,
    interface_type varchar(255),
    name varchar(255),
    host varchar(255),
    username varchar(255),
    password varchar(255),
    from_address varchar(255),
    modified_date datetime default CURRENT_TIMESTAMP,
    ignore_cert_errors BOOLEAN
);
INSERT INTO smtp (
    id,
    user_id,
    interface_type,
    name,
    host,
    username,
    password,
    from_address,
    modified_date,
    ignore_cert_errors
)
SELECT
    id,
    user_id,
    interface_type,
    name,
    host,
    username,
    password,
    from_address,
    modified_date,
    ignore_cert_errors
FROM smtp_with_ciphertext;
DROP TABLE smtp_with_ciphertext;
