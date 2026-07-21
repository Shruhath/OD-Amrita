-- 0005_events.sql — normalized branch reference data and event lifecycle.

-- +goose Up

CREATE TABLE branches (
    id         uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    code       text        NOT NULL UNIQUE CHECK (btrim(code) <> ''),
    name       text        NOT NULL CHECK (btrim(name) <> ''),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE events (
    id                  uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    name                text        NOT NULL CHECK (btrim(name) <> ''),
    event_date          date        NOT NULL,
    status              text        NOT NULL DEFAULT 'active'
                                    CHECK (status IN ('active', 'archived')),
    archived_at         timestamptz,
    archived_by_user_id uuid        REFERENCES users(id) ON DELETE RESTRICT,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT events_archive_state_ck CHECK (
        (status = 'active' AND archived_at IS NULL AND archived_by_user_id IS NULL)
        OR
        (status = 'archived' AND archived_at IS NOT NULL AND archived_by_user_id IS NOT NULL)
    )
);

CREATE TABLE event_branches (
    event_id  uuid        NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
    branch_id uuid        NOT NULL REFERENCES branches(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (event_id, branch_id)
);

CREATE INDEX events_list_order_idx
    ON events (event_date DESC, created_at DESC, id DESC);
CREATE INDEX events_status_list_order_idx
    ON events (status, event_date DESC, created_at DESC, id DESC);
CREATE INDEX event_branches_branch_id_idx
    ON event_branches (branch_id, event_id);

CREATE FUNCTION reject_archived_event_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    -- The owner must retain a cleanup/migration escape hatch; application
    -- traffic always uses od_runtime and cannot bypass this guard.
    IF current_user <> 'od_migrator' AND OLD.status = 'archived' THEN
        RAISE EXCEPTION 'archived event % is immutable', OLD.id
            USING ERRCODE = '55000';
    END IF;
    RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
END;
$$;

CREATE TRIGGER events_immutable_when_archived
BEFORE UPDATE OR DELETE ON events
FOR EACH ROW EXECUTE FUNCTION reject_archived_event_mutation();

CREATE FUNCTION require_active_event_for_branch_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    target_status text;
BEGIN
    IF current_user = 'od_migrator' THEN
        RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
    END IF;

    IF TG_OP = 'UPDATE' AND OLD.event_id <> NEW.event_id THEN
        SELECT status INTO target_status FROM events WHERE id = OLD.event_id;
        IF target_status = 'archived' THEN
            RAISE EXCEPTION 'archived event % branch membership is immutable', OLD.event_id
                USING ERRCODE = '55000';
        END IF;
    END IF;

    SELECT status INTO target_status
    FROM events
    WHERE id = CASE WHEN TG_OP = 'DELETE' THEN OLD.event_id ELSE NEW.event_id END;

    IF target_status = 'archived' THEN
        RAISE EXCEPTION 'archived event branch membership is immutable'
            USING ERRCODE = '55000';
    END IF;
    RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
END;
$$;

CREATE TRIGGER event_branches_active_guard
BEFORE INSERT OR UPDATE OR DELETE ON event_branches
FOR EACH ROW EXECUTE FUNCTION require_active_event_for_branch_mutation();

GRANT SELECT, INSERT, UPDATE, DELETE ON branches TO od_runtime;
GRANT SELECT, INSERT, UPDATE ON events TO od_runtime;
GRANT SELECT, INSERT, UPDATE ON event_branches TO od_runtime;
REVOKE DELETE ON events, event_branches FROM od_runtime;

-- +goose Down

DROP TABLE event_branches;
DROP TABLE events;
DROP TABLE branches;
DROP FUNCTION require_active_event_for_branch_mutation();
DROP FUNCTION reject_archived_event_mutation();
