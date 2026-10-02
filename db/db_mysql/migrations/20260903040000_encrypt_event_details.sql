
-- +goose Up
-- SQL in section 'Up' is executed when this migration is applied
--
-- MEDIUMBLOB (16 MiB), explicitly NOT a standard BLOB: the historical
-- `details BLOB` column caps at 65,535 bytes, and a legacy plaintext row
-- written before this PR (itself bounded only by that 64 KiB BLOB cap, with
-- no application-level bound) produces, once base64-framed into a v1
-- envelope, a ciphertext comfortably larger than 64 KiB. A standard BLOB
-- column would silently truncate such a row during migration; MEDIUMBLOB
-- does not. See models/event_details_credentials.go for the full
-- plaintext-size-bound rationale that keeps *new* writes well inside both
-- the legacy BLOB cap (for rollback compatibility) and, with margin, under
-- 64 KiB even as an envelope.
ALTER TABLE `events` ADD COLUMN `details_ciphertext` MEDIUMBLOB NULL;

-- Durable, restart-surviving migration-state marker (see
-- models/event_details_migration.go). Seeded as 'migrating': from the
-- moment this migration applies, new writes are ciphertext-only while
-- legacy rows from before this deploy remain read-compatible until an
-- operator explicitly finalizes.
CREATE TABLE `event_details_migration_state` (
    `id` INTEGER PRIMARY KEY,
    `state` VARCHAR(20) NOT NULL DEFAULT 'migrating',
    `updated_at` DATETIME NULL,
    CONSTRAINT `event_details_migration_state_single_row` CHECK (`id` = 1)
) ENGINE=InnoDB;
INSERT INTO `event_details_migration_state` (`id`, `state`, `updated_at`)
VALUES (1, 'migrating', CURRENT_TIMESTAMP);

-- +goose Down
-- SQL section 'Down' is executed when this migration is rolled back
--
-- Refuse to drop details_ciphertext while any row still carries ciphertext,
-- and refuse it if the migration marker says FINALIZED without an explicit
-- --rollback-event-details run first (rollback always resets FINALIZED back
-- to MIGRATING once it verifiably empties the MIGRATED/BOTH sets -- see
-- RollbackEventDetailsBatch). Same collide-on-primary-key guard mechanism as
-- the SQLite Down and the existing webhook/SMTP/API-key-verifier
-- precedents.
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
DROP TEMPORARY TABLE events_ciphertext_must_be_rolled_back_before_schema_down;

DROP TABLE `event_details_migration_state`;

ALTER TABLE `events` DROP COLUMN `details_ciphertext`;
