
-- +goose Up
-- SQL in section 'Up' is executed when this migration is applied
ALTER TABLE events ADD COLUMN details_ciphertext BLOB;

-- Durable, restart-surviving migration-state marker (see
-- models/event_details_migration.go). The row is seeded as 'migrating'
-- because from the moment this migration applies, new writes are
-- ciphertext-only (see AddEvent/persistEventWithEncryptedDetails) while
-- legacy rows written by an older binary may still exist and remain
-- read-compatible until an operator explicitly finalizes.
CREATE TABLE event_details_migration_state (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    state VARCHAR(20) NOT NULL DEFAULT 'migrating',
    updated_at DATETIME
);
INSERT INTO event_details_migration_state (id, state, updated_at)
VALUES (1, 'migrating', CURRENT_TIMESTAMP);

-- +goose Down
-- SQL section 'Down' is executed when this migration is rolled back
--
-- Refuse to drop details_ciphertext while any row still carries ciphertext
-- (the only remaining copy of that row's migrated data), and refuse it if
-- the migration marker says FINALIZED without an explicit
-- --rollback-event-details run first (rollback always resets FINALIZED back
-- to MIGRATING once it verifiably empties the MIGRATED/BOTH sets -- see
-- RollbackEventDetailsBatch). Both conditions are checked by attempting to
-- insert a second row with the same guard primary key: if either SELECT
-- matches, the INSERT collides and this whole Down migration fails/rolls
-- back, exactly like the equivalent webhook/SMTP credential guards.
DROP TABLE IF EXISTS events_ciphertext_must_be_rolled_back_before_schema_down;
CREATE TEMPORARY TABLE events_ciphertext_must_be_rolled_back_before_schema_down (
    guard INTEGER PRIMARY KEY
);
INSERT INTO events_ciphertext_must_be_rolled_back_before_schema_down (guard) VALUES (1);
INSERT INTO events_ciphertext_must_be_rolled_back_before_schema_down (guard)
SELECT 1
FROM events
WHERE details_ciphertext IS NOT NULL
LIMIT 1;
INSERT INTO events_ciphertext_must_be_rolled_back_before_schema_down (guard)
SELECT 1
FROM event_details_migration_state
WHERE id = 1 AND state = 'finalized'
LIMIT 1;
DROP TABLE events_ciphertext_must_be_rolled_back_before_schema_down;

DROP TABLE event_details_migration_state;

-- SQLite cannot remove a column in place on every bundled driver version
-- this repository targets, and the autoincrement high-water mark
-- (sqlite_sequence) must survive the rebuild exactly as the SMTP/webhook
-- credential schema Down migrations already do, so a deleted high-ID event
-- is never reused by a later insert.
CREATE TEMPORARY TABLE events_sequence_before_schema_down (
    high_water_mark INTEGER NOT NULL
);
INSERT INTO events_sequence_before_schema_down (high_water_mark)
SELECT CASE
    WHEN COALESCE((SELECT seq FROM sqlite_sequence WHERE name = 'events'), 0)
         > COALESCE((SELECT MAX(id) FROM events), 0)
    THEN COALESCE((SELECT seq FROM sqlite_sequence WHERE name = 'events'), 0)
    ELSE COALESCE((SELECT MAX(id) FROM events), 0)
END;

ALTER TABLE events RENAME TO events_with_ciphertext;
CREATE TABLE events (
    id integer primary key autoincrement,
    campaign_id bigint,
    email varchar(255),
    time datetime,
    message varchar(255),
    details BLOB
);
INSERT INTO events (
    id,
    campaign_id,
    email,
    time,
    message,
    details
)
SELECT
    id,
    campaign_id,
    email,
    time,
    message,
    details
FROM events_with_ciphertext;
DROP TABLE events_with_ciphertext;

UPDATE sqlite_sequence
SET seq = (SELECT high_water_mark FROM events_sequence_before_schema_down)
WHERE name = 'events';
INSERT INTO sqlite_sequence (name, seq)
SELECT 'events', high_water_mark
FROM events_sequence_before_schema_down
WHERE NOT EXISTS (
    SELECT 1 FROM sqlite_sequence WHERE name = 'events'
);
DROP TABLE events_sequence_before_schema_down;
