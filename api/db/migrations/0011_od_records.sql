-- 0011_od_records.sql — synchronous one-event MVP attendance marks.

-- +goose Up

ALTER TABLE categories
    ADD CONSTRAINT categories_event_id_id_unique UNIQUE (event_id, id);

CREATE TABLE od_records (
    id                 uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    student_id         uuid        NOT NULL REFERENCES students(id) ON DELETE RESTRICT,
    event_id           uuid        NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
    category_id        uuid        NOT NULL,
    slot               integer     NOT NULL CHECK (slot BETWEEN 1 AND 12),
    status             text        NOT NULL DEFAULT 'present' CHECK (status IN ('present')),
    capture_method     text        NOT NULL CHECK (capture_method IN ('nfc', 'camera', 'search')),
    actor_user_id      uuid        NULL REFERENCES users(id) ON DELETE SET NULL,
    actor_role         text        NOT NULL CHECK (actor_role IN ('coordinator', 'teacher', 'admin')),
    server_received_at timestamptz NOT NULL DEFAULT now(),
    created_at         timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT od_records_category_event_fk
        FOREIGN KEY (event_id, category_id)
        REFERENCES categories(event_id, id)
        ON DELETE RESTRICT
);

CREATE UNIQUE INDEX uq_od_records_present_student_event_slot
    ON od_records (student_id, event_id, slot)
    WHERE status = 'present';

CREATE INDEX od_records_event_category_slot_idx
    ON od_records (event_id, category_id, slot);

CREATE INDEX od_records_student_event_slot_idx
    ON od_records (student_id, event_id, slot);

CREATE INDEX od_records_actor_event_slot_idx
    ON od_records (actor_user_id, event_id, slot);

CREATE TRIGGER od_records_active_event_guard
BEFORE INSERT OR UPDATE OR DELETE ON od_records
FOR EACH ROW EXECUTE FUNCTION require_active_event_for_child_mutation('event_id');

GRANT SELECT, INSERT, UPDATE ON od_records TO od_runtime;
REVOKE DELETE ON od_records FROM od_runtime;

-- +goose Down

DROP TABLE od_records;

ALTER TABLE categories
    DROP CONSTRAINT categories_event_id_id_unique;
