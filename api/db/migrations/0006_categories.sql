-- 0006_categories.sql — per-event categories, and the generalized archived-event
-- guard that 0005 promised.
--
-- 0005 shipped require_active_event_for_branch_mutation(), hard-wired to
-- event_branches.event_id. Four child tables end up needing identical logic
-- (event_branches, categories, coordinator assignments, camera-credit config),
-- so this migration replaces it with ONE TG_ARGV-parameterized function and
-- re-points event_branches at it. Story 2.1's existing event-branch immutability
-- tests are the regression proof for that re-pointing.

-- +goose Up

CREATE TABLE categories (
    id         uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id   uuid        NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
    -- `name ~ '\S'` rather than 0005's `btrim(name) <> ''`: single-argument
    -- btrim strips SPACES ONLY, so a tab-only name would pass that check while
    -- the Go service (strings.TrimSpace) considers it blank. Requiring at least
    -- one non-whitespace character closes that gap for every whitespace form.
    name       text        NOT NULL CHECK (name ~ '\S'),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- The DATABASE-level truth behind AC 2. The service pre-check is a nicety; two
-- concurrent creates race straight past a SELECT-then-INSERT, and only this
-- index makes "unique within the event" true under concurrency. Normalized the
-- same way the service normalizes: whitespace-trimmed, case-folded. The trim set
-- is explicit for the same reason as the CHECK above.
CREATE UNIQUE INDEX uq_categories_event_id_name
    ON categories (event_id, lower(btrim(name, E' \t\r\n')));

-- Serves the deterministic per-event list read (name ASC, id ASC).
CREATE INDEX categories_event_id_name_idx
    ON categories (event_id, name ASC, id ASC);

-- +goose StatementBegin
CREATE FUNCTION require_active_event_for_child_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    owner_column  text;
    old_event_id  uuid;
    new_event_id  uuid;
    target_status text;
BEGIN
    -- Which column names the owning event is the trigger's argument, so one
    -- function serves every child table (event_branches today; coordinator
    -- assignments and camera-credit config next).
    owner_column := TG_ARGV[0];

    -- The owner must retain a cleanup/migration escape hatch; application
    -- traffic always uses od_runtime and cannot bypass this guard.
    IF current_user = 'od_migrator' THEN
        RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
    END IF;

    IF TG_OP <> 'INSERT' THEN
        old_event_id := (to_jsonb(OLD) ->> owner_column)::uuid;
    END IF;
    IF TG_OP <> 'DELETE' THEN
        new_event_id := (to_jsonb(NEW) ->> owner_column)::uuid;
    END IF;

    -- An UPDATE that re-parents a child is the same violation wearing a
    -- different hat: BOTH the row's old owner and its new owner must be active,
    -- or an archived event could be edited by moving rows out of it (or a closed
    -- aggregate could be grown by moving rows into it).
    IF TG_OP = 'UPDATE' AND old_event_id IS DISTINCT FROM new_event_id THEN
        SELECT status INTO target_status FROM events WHERE id = old_event_id;
        IF target_status = 'archived' THEN
            RAISE EXCEPTION 'archived event % is a closed aggregate: % is immutable', old_event_id, TG_TABLE_NAME
                USING ERRCODE = '55000';
        END IF;
    END IF;

    SELECT status INTO target_status
    FROM events
    WHERE id = CASE WHEN TG_OP = 'DELETE' THEN old_event_id ELSE new_event_id END;

    IF target_status = 'archived' THEN
        RAISE EXCEPTION 'archived event is a closed aggregate: % is immutable', TG_TABLE_NAME
            USING ERRCODE = '55000';
    END IF;
    RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
END;
$$;
-- +goose StatementEnd

DROP TRIGGER event_branches_active_guard ON event_branches;
DROP FUNCTION require_active_event_for_branch_mutation();

CREATE TRIGGER event_branches_active_guard
BEFORE INSERT OR UPDATE OR DELETE ON event_branches
FOR EACH ROW EXECUTE FUNCTION require_active_event_for_child_mutation('event_id');

CREATE TRIGGER categories_active_event_guard
BEFORE INSERT OR UPDATE OR DELETE ON categories
FOR EACH ROW EXECUTE FUNCTION require_active_event_for_child_mutation('event_id');

-- Same posture 0005 gave events: DML but never DELETE. Categories will be
-- referenced by coordinator assignments, OD records, disputes and tallies, so
-- no-hard-delete applies from day one and is enforced by a REVOKE rather than by
-- remembering not to write the query.
GRANT SELECT, INSERT, UPDATE ON categories TO od_runtime;
REVOKE DELETE ON categories FROM od_runtime;

-- +goose Down

DROP TABLE categories;

DROP TRIGGER event_branches_active_guard ON event_branches;
DROP FUNCTION require_active_event_for_child_mutation();

-- Restore 0005's exact state, or the database cannot roll forward again — the
-- failure mode that makes an incident worse at the moment you most need to revert.
-- +goose StatementBegin
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
-- +goose StatementEnd

CREATE TRIGGER event_branches_active_guard
BEFORE INSERT OR UPDATE OR DELETE ON event_branches
FOR EACH ROW EXECUTE FUNCTION require_active_event_for_branch_mutation();
