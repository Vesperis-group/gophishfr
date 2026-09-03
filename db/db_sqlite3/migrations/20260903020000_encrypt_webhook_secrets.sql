-- +goose Up
ALTER TABLE webhooks
    ADD COLUMN secret_ciphertext VARCHAR(2048) NOT NULL DEFAULT '';

-- +goose Down
-- Ciphertext must be explicitly rolled back to the legacy secret column first.
DROP TABLE IF EXISTS webhooks_ciphertext_must_be_rolled_back_before_schema_down;
CREATE TEMPORARY TABLE webhooks_ciphertext_must_be_rolled_back_before_schema_down (
    guard INTEGER PRIMARY KEY
);
INSERT INTO webhooks_ciphertext_must_be_rolled_back_before_schema_down (guard) VALUES (1);
INSERT INTO webhooks_ciphertext_must_be_rolled_back_before_schema_down (guard)
SELECT 1
FROM webhooks
WHERE secret_ciphertext <> ''
LIMIT 1;
DROP TABLE webhooks_ciphertext_must_be_rolled_back_before_schema_down;

CREATE TEMPORARY TABLE webhooks_sequence_before_schema_down (
    high_water_mark INTEGER NOT NULL
);
INSERT INTO webhooks_sequence_before_schema_down (high_water_mark)
SELECT CASE
    WHEN COALESCE((SELECT seq FROM sqlite_sequence WHERE name = 'webhooks'), 0)
         > COALESCE((SELECT MAX(id) FROM webhooks), 0)
    THEN COALESCE((SELECT seq FROM sqlite_sequence WHERE name = 'webhooks'), 0)
    ELSE COALESCE((SELECT MAX(id) FROM webhooks), 0)
END;

ALTER TABLE webhooks RENAME TO webhooks_with_ciphertext;
CREATE TABLE webhooks (
    id integer primary key autoincrement,
    name varchar(255),
    url varchar(1000),
    secret varchar(255),
    is_active boolean default 0
);
INSERT INTO webhooks (
    id,
    name,
    url,
    secret,
    is_active
)
SELECT
    id,
    name,
    url,
    secret,
    is_active
FROM webhooks_with_ciphertext;
DROP TABLE webhooks_with_ciphertext;

UPDATE sqlite_sequence
SET seq = (SELECT high_water_mark FROM webhooks_sequence_before_schema_down)
WHERE name = 'webhooks';
INSERT INTO sqlite_sequence (name, seq)
SELECT 'webhooks', high_water_mark
FROM webhooks_sequence_before_schema_down
WHERE NOT EXISTS (
    SELECT 1 FROM sqlite_sequence WHERE name = 'webhooks'
);
DROP TABLE webhooks_sequence_before_schema_down;
