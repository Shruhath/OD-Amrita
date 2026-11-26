-- 0006_categories.sql — per-event categories, and the generalized archived-event
-- guard that 0005 promised.
--
-- 0005 shipped require_active_event_for_branch_mutation(), hard-wired to
-- event_branches.event_id. Four child tables end up needing identical logic
-- (event_branches, categories, coordinator assignments, camera-credit config),
-- so this migration replaces it with ONE TG_ARGV-parameterized function and
-- re-points event_branches at it. The existing event-branch immutability
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
    -- The length bound belongs here as well as in the service. The service's own
    -- reasoning for the unique index applies verbatim: a pre-check is not the
    -- guarantee, because od_migrator (the escape hatch below), a future bulk
    -- import, or the DB browser can all write rows the handler never sees, and the
    -- category list endpoint is unpaginated by contract. 120 matches
    -- CreateCategoryRequest.maxLength; char_length counts characters, which is
    -- what utf8.RuneCountInString counts on the Go side.
    CONSTRAINT categories_name_length CHECK (char_length(name) <= 120),
    created_at timestamptz NOT NULL DEFAULT now(),
    -- Maintained by the UPDATE statement, not by a trigger — the convention set by
    -- events.sql:64 and users.sql:23, which both write `updated_at = now()` in the
    -- query. Categories are create+list only today, so nothing updates this yet; a
    -- future rename feature must set it in its own UPDATE to match.
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
    --
    -- Generalizing this function introduced one failure mode the single-purpose
    -- version could not have: a trigger attached with no argument, or with a
    -- typo'd column, would leave owner_column NULL. `to_jsonb(NEW) ->> NULL` is
    -- NULL, `WHERE id = NULL` matches nothing, target_status stays NULL, and
    -- `NULL = 'archived'` is not true — so the guard would appear in pg_trigger
    -- and enforce NOTHING. Fail closed on a misconfigured trigger instead.
    IF TG_NARGS <> 1 THEN
        RAISE EXCEPTION
            'require_active_event_for_child_mutation on % needs exactly one argument (the owning event column), got %',
            TG_TABLE_NAME, TG_NARGS
            USING ERRCODE = '39P01';
    END IF;

    owner_column := TG_ARGV[0];

    -- A column that is not on the row is the same misconfiguration wearing a
    -- different hat. Checked against the row itself so it costs nothing extra.
    -- jsonb_exists(...) rather than the `?` operator: `?` is a placeholder to
    -- several drivers, and this body will be read and re-run by hand during an
    -- incident. The function form is unambiguous everywhere.
    IF NOT jsonb_exists(to_jsonb(CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END), owner_column) THEN
        RAISE EXCEPTION
            'require_active_event_for_child_mutation: table % has no column %',
            TG_TABLE_NAME, owner_column
            USING ERRCODE = '39P01';
    END IF;

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
        SELECT status INTO target_status FROM events WHERE id = old_event_id FOR SHARE;
        IF target_status = 'archived' THEN
            RAISE EXCEPTION 'archived event % is a closed aggregate: % is immutable', old_event_id, TG_TABLE_NAME
                USING ERRCODE = '55000';
        END IF;
    END IF;

    -- FOR SHARE, not a bare SELECT. Without a row lock this guard loses a
    -- routine two-admin race: the categories.event_id foreign key takes only
    -- FOR KEY SHARE on the events row, and archiving is an UPDATE of a non-key
    -- column, which takes FOR NO KEY UPDATE — the two DO NOT conflict. Under
    -- READ COMMITTED the create would read the pre-archive snapshot, both
    -- transactions would commit, and an archived event would own a category
    -- created after archived_at. FOR SHARE conflicts with FOR NO KEY UPDATE, so
    -- the archive blocks until this write finishes and then the loser sees the
    -- archived status. This is the same reasoning that made uniqueness an index
    -- rather than a SELECT-then-INSERT, applied to the other invariant.
    SELECT status INTO target_status
    FROM events
    WHERE id = CASE WHEN TG_OP = 'DELETE' THEN old_event_id ELSE new_event_id END
    FOR SHARE;

    IF target_status = 'archived' THEN
        RAISE EXCEPTION 'archived event is a closed aggregate: % is immutable', TG_TABLE_NAME
            USING ERRCODE = '55000';
    END IF;
    -- target_status IS NULL (no such event) is deliberately NOT an error here:
    -- this is a BEFORE trigger, so the foreign key has not fired yet, and
    -- raising would mask the FK's 23503 — which the service maps to a truthful
    -- EVENT_NOT_FOUND 404 — behind a misleading 55000/EVENT_ARCHIVED.
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
