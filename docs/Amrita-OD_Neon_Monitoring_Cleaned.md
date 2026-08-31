# Amrita-OD — Neon Database Monitoring

## Project Context

- **Owner:** Shruhath Reddy
- **Project:** Amrita-OD
- **Environment:** production
- **Database:** neondb
- **Database type:** Postgres database
- **Compute:** Primary — Active
- **Status:** All OK

## Monitoring Sections

- Metrics
- Active queries
- Query performance
- System operations
- Data API Advisors

## Query Performance

**Total entries: 100**

### 1. `ProvisionUser`

- **Role:** `od\_runtime`
- **Calls:** 2157
- **Average time:** 8 ms
- **Total time:** 17 s

```sql
-- name: ProvisionUser :one INSERT INTO users (supabase_subject, email, name, role, last_login_at) VALUES ($1, lower($2), $3, $4, now()) ON CONFLICT (supabase_subject) DO UPDATE SET email = lower(excluded.email), name = excluded.name, last_login_at = now(), updated_at = now() RETURNING id::text, supabase_subject, email, name, role, (xmax = $5) AS inserted
```

### 2. `DebitCaptureCredit`

- **Role:** `od\_runtime`
- **Calls:** 185
- **Average time:** 28.8 ms
- **Total time:** 5 s

```sql
-- name: DebitCaptureCredit :one WITH assignment AS ( SELECT eca.event_id, eca.user_id FROM event_coordinator_assignments eca WHERE eca.event_id = $1 AND eca.user_id = $2 FOR UPDATE ), effective AS ( SELECT COALESCE(cfg.credit_limit, $4)::integer AS credit_limit FROM events e JOIN assignment ON assignment.event_id = e.id LEFT JOIN event_capture_credit_config cfg ON cfg.event_id = e.id ), debited AS ( INSERT INTO event_capture_credit_usage (event_id, user_id, usage_date, consumed, updated_at) SELECT assignment.event_id, assignment.user_id, $3, $5, now() FROM effective, assignment WHERE effective.credit_limit > $6 ON CONFLICT (event_id, user_id, usage_date) DO UPDATE SET consumed = event_capture_credit_usage.consumed + $7, updated_at = now() WHERE event_capture_credit_usage.consumed < (SELECT credit_limit FROM effective) RETURNING consumed ) SELECT effective.credit_limit, debited.consumed, GREATEST(effective.credit_limit - debited.consumed, $8)::integer AS remaining, (debited.consumed >= effective.credit_limit)::boolean AS exhausted FROM effective, debited
```

### 3. `SearchStudents`

- **Role:** `od\_runtime`
- **Calls:** 2332
- **Average time:** 1.5 ms
- **Total time:** 3 s

```sql
-- name: SearchStudents :many SELECT s.id, s.roll_no, s.name, b.code AS branch, s.section, s.batch FROM students s JOIN branches b ON b.id = s.branch_id WHERE lower(s.roll_no || $5 || s.name) ~~ ALL ($1::text[]) ORDER BY (s.roll_no = $2::text) DESC, -- Then whoever the query STARTS a word of, so typing a first name still -- puts that person above someone who merely contains the letters mid-word. -- The leading space is what makes a match at position one count as a word -- start too. (($6 || lower(s.roll_no || $7 || s.name)) LIKE $3::text ESCAPE $8) DESC, s.roll_no ASC, lower(s.name) ASC, s.id ASC LIMIT $4::bigint
```

### 4. `DebitCaptureCredit`

- **Role:** `od\_runtime`
- **Calls:** 13
- **Average time:** 190 ms
- **Total time:** 2 s

```sql
-- name: DebitCaptureCredit :one WITH assignment AS ( SELECT eca.event_id, eca.user_id FROM event_coordinator_assignments eca WHERE eca.event_id = $1 AND eca.user_id = $2 FOR UPDATE ), effective AS ( SELECT COALESCE(cfg.credit_limit, $4)::integer AS credit_limit FROM events e JOIN assignment ON assignment.event_id = e.id LEFT JOIN event_capture_credit_config cfg ON cfg.event_id = e.id ), debited AS ( INSERT INTO event_capture_credit_usage (event_id, user_id, usage_date, consumed, updated_at) SELECT assignment.event_id, assignment.user_id, $3, $5, now() FROM effective, assignment WHERE effective.credit_limit > $6 ON CONFLICT (event_id, user_id, usage_date) DO UPDATE SET consumed = event_capture_credit_usage.consumed + $7, updated_at = now() WHERE event_capture_credit_usage.consumed < (SELECT credit_limit FROM effective) RETURNING consumed ) SELECT effective.credit_limit, debited.consumed, GREATEST(effective.credit_limit - debited.consumed, $8)::integer AS remaining, (debited.consumed >= effective.credit_limit)::boolean AS exhausted FROM effective, debited
```

### 5. `GetEvent`

- **Role:** `od\_runtime`
- **Calls:** 7914
- **Average time:** 0.1 ms
- **Total time:** 903.9 ms

```sql
-- name: GetEvent :one SELECT e.id, e.name, e.event_date, e.end_date, e.status, e.archived_at, e.created_at, COALESCE( jsonb_agg( jsonb_build_object($2, b.id, $3, b.code, $4, b.name) ORDER BY b.code ASC, b.id ASC ) FILTER (WHERE b.id IS NOT NULL), $5::jsonb )::text AS branches_json FROM events e LEFT JOIN event_branches eb ON eb.event_id = e.id LEFT JOIN branches b ON b.id = eb.branch_id WHERE e.id = $1 GROUP BY e.id
```

### 6. `ListAttendanceRecords`

- **Role:** `od\_runtime`
- **Calls:** 1099
- **Average time:** 0.4 ms
- **Total time:** 469.3 ms

```sql
-- name: ListAttendanceRecords :many SELECT r.id, r.student_id, s.roll_no, s.name AS student_name, b.code AS branch, s.section, s.batch, r.event_id, c.id AS category_id, c.name AS category_name, c.created_at AS category_created_at, r.slot, r.od_date, r.status, r.reason_note, r.capture_method, r.actor_user_id, u.email AS actor_email, u.name AS actor_name, r.actor_role, r.server_received_at FROM od_records r JOIN students s ON s.id = r.student_id JOIN branches b ON b.id = s.branch_id JOIN categories c ON c.id = r.category_id LEFT JOIN users u ON u.id = r.actor_user_id WHERE r.event_id = $1 AND ($2::uuid IS NULL OR r.category_id = $2) AND ($3::integer IS NULL OR r.slot = $3) AND ($4::date IS NULL OR r.od_date = $4) AND ($5::text IS NULL OR s.section = $5) AND ($6::uuid IS NULL OR r.actor_user_id = $6) AND ( -- BRANCH SCOPE. NULL means "no scoping", which is the admin view. A -- teacher passes their own id and sees only students in the branches they -- teach, matching the rule that already governs which students they may -- MARK — a teacher who cannot mark a student has no reason to read that -- student's record either. -- -- One query with an optional predicate rather than two, so the admin and -- teacher views can never drift in their ordering, their filters or their -- definition of "present". $7::uuid IS NULL OR EXISTS ( SELECT $10 FROM user_branches ub JOIN user_category_assignments uca ON uca.user_id = ub.user_id WHERE ub.user_id = $7 AND ub.branch_id = s.branch_id AND uca.category_id = r.category_id ) ) ORDER BY r.od_date DESC, r.slot ASC, r.server_received_at DESC, s.roll_no ASC, r.id ASC LIMIT $9::bigint OFFSET $8::bigint
```

### 7. `InsertAuditLog`

- **Role:** `od\_runtime`
- **Calls:** 2740
- **Average time:** 0.2 ms
- **Total time:** 449.4 ms

```sql
-- name: InsertAuditLog :exec INSERT INTO audit_log (action, actor_account, actor_role, target_table, target_id, payload) VALUES ($1, $2, $3, $4, $5, $6)
```

### 8. `GetUserBySubject`

- **Role:** `od\_runtime`
- **Calls:** 13571
- **Average time:** 0 ms
- **Total time:** 415.5 ms

```sql
-- name: GetUserBySubject :one SELECT id, supabase_subject, email, name, role, created_at, updated_at, last_login_at FROM users WHERE supabase_subject = $1
```

### 9. `InsertODPresentRecord`

- **Role:** `od\_runtime`
- **Calls:** 319
- **Average time:** 1.1 ms
- **Total time:** 354.8 ms

```sql
-- name: InsertODPresentRecord :one INSERT INTO od_records ( student_id, event_id, category_id, slot, od_date, reason_note, capture_method, actor_user_id, actor_role, server_received_at ) VALUES ( $1, $2, $3, $4, $5, $6, $7, $8, $9, $10 ) RETURNING id, student_id, event_id, category_id, slot, od_date, reason_note, capture_method, server_received_at
```

### 10. `IsTokenRevoked`

- **Role:** `od\_runtime`
- **Calls:** 15766
- **Average time:** 0 ms
- **Total time:** 321.9 ms

```sql
-- name: IsTokenRevoked :one SELECT EXISTS ( SELECT $4 FROM auth_session_revocations WHERE supabase_subject = $1 AND ( (scope = $5 AND session_id = $2::uuid) OR (scope = $6 AND date_trunc($7, revoked_after) > $3) ) ) AS revoked
```

### 11. `GetMarkEvent`

- **Role:** `od\_runtime`
- **Calls:** 8862
- **Average time:** 0 ms
- **Total time:** 305.8 ms

```sql
-- name: GetMarkEvent :one SELECT id, event_date, end_date, status FROM events WHERE id = $1
```

### 12. `GetCaptureCreditState`

- **Role:** `od\_runtime`
- **Calls:** 6798
- **Average time:** 0 ms
- **Total time:** 271.4 ms

```sql
-- name: GetCaptureCreditState :one WITH effective AS ( SELECT COALESCE(cfg.credit_limit, $4)::integer AS credit_limit FROM events e LEFT JOIN event_capture_credit_config cfg ON cfg.event_id = e.id WHERE e.id = $1 ), usage AS ( SELECT COALESCE(u.consumed, $5)::integer AS consumed FROM effective LEFT JOIN event_capture_credit_usage u ON u.event_id = $1 AND u.user_id = $2 AND u.usage_date = $3 ) SELECT effective.credit_limit, usage.consumed, GREATEST(effective.credit_limit - usage.consumed, $6)::integer AS remaining, (usage.consumed >= effective.credit_limit)::boolean AS exhausted FROM effective, usage
```

### 13. `ListPublicDayODRecords`

- **Role:** `od\_runtime`
- **Calls:** 1070
- **Average time:** 0.3 ms
- **Total time:** 267.5 ms

```sql
-- name: ListPublicDayODRecords :many SELECT s.roll_no, s.name AS student_name, r.slot, c.id AS category_id, c.name AS category_name, e.name AS event_name FROM od_records r JOIN students s ON s.id = r.student_id JOIN categories c ON c.id = r.category_id JOIN events e ON e.id = r.event_id WHERE r.od_date = $1 AND r.status = $5 AND c.visible_on_public_dashboard = $6 AND ($2::integer IS NULL OR r.slot = $2) AND ($3::uuid IS NULL OR r.category_id = $3) ORDER BY s.roll_no ASC, r.slot ASC, c.name ASC LIMIT $4::bigint
```

### 14. `ListCategoriesByEvent`

- **Role:** `od\_runtime`
- **Calls:** 5497
- **Average time:** 0 ms
- **Total time:** 231.6 ms

```sql
-- name: ListCategoriesByEvent :many SELECT id, event_id, name, created_at FROM categories WHERE event_id = $1 ORDER BY name ASC, id ASC
```

### 15. `GetAppSetting`

- **Role:** `od\_runtime`
- **Calls:** 4884
- **Average time:** 0 ms
- **Total time:** 228.8 ms

```sql
-- name: GetAppSetting :one SELECT key, value, updated_at FROM app_settings WHERE key = $1
```

### 16. `CountMarksForCategory`

- **Role:** `od\_runtime`
- **Calls:** 6818
- **Average time:** 0 ms
- **Total time:** 209 ms

```sql
-- name: CountMarksForCategory :one SELECT count(*)::bigint AS category_total, (count(*) FILTER (WHERE actor_user_id = $1))::bigint AS mine FROM od_records WHERE event_id = $2 AND category_id = $3 AND od_date = $4 AND status = $5
```

### 17. `CoordinatorAssignedToEvent`

- **Role:** `od\_runtime`
- **Calls:** 7722
- **Average time:** 0 ms
- **Total time:** 186.2 ms

```sql
-- name: CoordinatorAssignedToEvent :one SELECT EXISTS( SELECT $3 FROM event_coordinator_assignments WHERE user_id = $1 AND event_id = $2 )::boolean
```

### 18. `ListSectionCardStatus`

- **Role:** `od\_runtime`
- **Calls:** 687
- **Average time:** 0.3 ms
- **Total time:** 182.4 ms

```sql
-- name: ListSectionCardStatus :many SELECT s.id AS student_id, (t.id IS NOT NULL)::boolean AS issued, t.verified_at FROM students s LEFT JOIN student_card_tokens t ON t.student_id = s.id AND t.version = $1::text WHERE s.branch_id = $2 AND s.batch = $3::text AND s.section = $4::text ORDER BY s.roll_no
```

### 19. `SQL / System Query`

- **Role:** `neondb\_owner`
- **Calls:** 34
- **Average time:** 5 ms
- **Total time:** 169.9 ms

```sql
SELECT grantor, grantee, table_schema AS "schema", table_name AS "table", privilege_type AS "type", CASE is_grantable WHEN $1 THEN $2 ELSE $3 END AS "isGrantable" FROM information_schema.role_table_grants WHERE table_schema IN ($4) ORDER BY pg_catalog.lower(table_schema), pg_catalog.lower(table_name), pg_catalog.lower(grantee)
```

### 20. `CountAttendanceRecords`

- **Role:** `od\_runtime`
- **Calls:** 1101
- **Average time:** 0.1 ms
- **Total time:** 136.1 ms

```sql
-- name: CountAttendanceRecords :one SELECT count(*)::bigint FROM od_records r JOIN students s ON s.id = r.student_id WHERE r.event_id = $1 AND ($2::uuid IS NULL OR r.category_id = $2) AND ($3::integer IS NULL OR r.slot = $3) AND ($4::date IS NULL OR r.od_date = $4) AND ($5::text IS NULL OR s.section = $5) AND ($6::uuid IS NULL OR r.actor_user_id = $6) AND ( -- BRANCH SCOPE. NULL means "no scoping", which is the admin view. A -- teacher passes their own id and sees only students in the branches they -- teach, matching the rule that already governs which students they may -- MARK — a teacher who cannot mark a student has no reason to read that -- student's record either. -- -- One query with an optional predicate rather than two, so the admin and -- teacher views can never drift in their ordering, their filters or their -- definition of "present". $7::uuid IS NULL OR EXISTS ( SELECT $8 FROM user_branches ub JOIN user_category_assignments uca ON uca.user_id = ub.user_id WHERE ub.user_id = $7 AND ub.branch_id = s.branch_id AND uca.category_id = r.category_id ) )
```

### 21. `ListPublicDayCategories`

- **Role:** `od\_runtime`
- **Calls:** 1059
- **Average time:** 0.1 ms
- **Total time:** 117.4 ms

```sql
-- name: ListPublicDayCategories :many SELECT DISTINCT c.id, c.name FROM od_records r JOIN categories c ON c.id = r.category_id WHERE r.od_date = $1 AND r.status = $2 AND c.visible_on_public_dashboard = $3 ORDER BY c.name ASC
```

### 22. `SQL / System Query`

- **Role:** `neondb\_owner`
- **Calls:** 30
- **Average time:** 3.6 ms
- **Total time:** 107.4 ms

```sql
SELECT oid, connamespace AS "schemaId", conrelid AS "tableId", conname AS "name", contype::text AS "type", pg_catalog.pg_get_constraintdef(oid) AS "definition", conindid AS "indexId", conkey AS "columnsOrdinals", confrelid AS "tableToId", confkey AS "columnsToOrdinals", confupdtype::text AS "onUpdate", confdeltype::text AS "onDelete" FROM pg_catalog.pg_constraint WHERE conrelid IN ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19) ORDER BY conrelid, contype, pg_catalog.lower(conname)
```

### 23. `ListCategoriesByEvent`

- **Role:** `od\_runtime`
- **Calls:** 2404
- **Average time:** 0 ms
- **Total time:** 102.5 ms

```sql
-- name: ListCategoriesByEvent :many SELECT id, event_id, name, visible_on_public_dashboard, created_at FROM categories WHERE event_id = $1 ORDER BY name ASC, id ASC
```

### 24. `UpsertStudentCardToken`

- **Role:** `od\_runtime`
- **Calls:** 418
- **Average time:** 0.2 ms
- **Total time:** 91.6 ms

```sql
-- name: UpsertStudentCardToken :one WITH upsert AS ( INSERT INTO student_card_tokens (student_id, version, token_sha256) VALUES ($1, $2, $3) ON CONFLICT (student_id, version) DO UPDATE SET token_sha256 = EXCLUDED.token_sha256, updated_at = now(), -- A CHANGED token means the card in the operator's hand now holds a -- stale one, so whatever was verified before is no longer true. Re- -- sealing the SAME student under the same key produces the SAME token -- (it is an HMAC of the roll number), so the guard below skips the row -- entirely and an ordinary re-write does not throw away a verification. verified_at = $4, verified_by_user_id = $5 WHERE student_card_tokens.token_sha256 IS DISTINCT FROM EXCLUDED.token_sha256 RETURNING id, (xmax = $6) AS inserted ) SELECT id, inserted FROM upsert
```

### 25. `SQL / System Query`

- **Role:** `neondb\_owner`
- **Calls:** 30
- **Average time:** 2.4 ms
- **Total time:** 71.6 ms

```sql
SELECT pg_class.oid, nspname as "schema", relname AS "name", am.amname AS "accessMethod", reloptions AS "with", pg_catalog.row_to_json(metadata.*) as "metadata" FROM pg_catalog.pg_class JOIN pg_catalog.pg_am am ON am.oid OPERATOR(pg_catalog.=) pg_class.relam JOIN pg_catalog.pg_namespace nsp ON nsp.oid OPERATOR(pg_catalog.=) pg_class.relnamespace JOIN LATERAL ( SELECT pg_catalog.pg_get_expr(indexprs, indrelid) AS "expression", pg_catalog.pg_get_expr(indpred, indrelid) AS "where", indrelid::int AS "tableId", indkey::int[] as "columnOrdinals", indoption::int[] as "options", indisunique as "isUnique", indisprimary as "isPrimary", array( SELECT pg_catalog.json_build_object( $1, opclass.oid, $2, pg_am.amname, $3, pg_opclass.opcname, $4, pg_opclass.opcdefault ) FROM pg_catalog.unnest(indclass) WITH ORDINALITY AS opclass(oid, ordinality) JOIN pg_catalog.pg_opclass ON opclass.oid OPERATOR(pg_catalog.=) pg_opclass.oid JOIN pg_catalog.pg_am ON pg_opclass.opcmethod OPERATOR(pg_catalog.=) pg_am.oid ORDER BY opclass.ordinality ) as "opclasses" FROM pg_catalog.pg_index WHERE pg_index.indexrelid OPERATOR(pg_catalog.=) pg_class.oid ) metadata ON $5 WHERE relkind OPERATOR(pg_catalog.=) ANY (ARRAY[$6, $7]) AND metadata."tableId" IN ($8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26) ORDER BY pg_catalog.lower(nspname), pg_catalog.lower(relname)
```

### 26. `CoordinatorAssignedToEventCategory`

- **Role:** `od\_runtime`
- **Calls:** 1964
- **Average time:** 0 ms
- **Total time:** 65.2 ms

```sql
-- name: CoordinatorAssignedToEventCategory :one SELECT EXISTS( SELECT $4 FROM event_coordinator_assignments eca JOIN user_category_assignments uca ON uca.user_id = eca.user_id JOIN categories c ON c.id = uca.category_id AND c.event_id = eca.event_id WHERE eca.user_id = $1 AND eca.event_id = $2 AND c.id = $3 )::boolean
```

### 27. `ListStudentsForCardPopulation`

- **Role:** `od\_runtime`
- **Calls:** 247
- **Average time:** 0.3 ms
- **Total time:** 64.5 ms

```sql
-- name: ListStudentsForCardPopulation :many SELECT s.id, s.roll_no, s.name, b.code AS branch, s.section, s.batch FROM students s JOIN branches b ON b.id = s.branch_id WHERE ($1::uuid IS NULL OR s.branch_id = $1::uuid) AND ($2::text = $6 OR s.batch = $2::text) AND ($3::text = $7 OR s.section = $3::text) ORDER BY b.code ASC, s.batch ASC, s.section ASC, s.roll_no ASC, s.id ASC LIMIT $5::bigint OFFSET $4::bigint
```

### 28. `SQL / System Query`

- **Role:** `neondb\_owner`
- **Calls:** 30
- **Average time:** 2.1 ms
- **Total time:** 62.4 ms

```sql
SELECT attrelid AS "tableId", relkind::text AS "kind", attname AS "name", attnum AS "ordinality", attnotnull AS "notNull", CASE WHEN attndims > $1 THEN attndims WHEN t.typcategory = $2 THEN $3 -- If it's an array type, default to 1 dimension ELSE $4 END as "dimensions", atttypid as "typeId", attgenerated::text as "generatedType", attidentity::text as "identityType", pg_catalog.format_type(atttypid, atttypmod) as "type", pg_catalog.pg_get_serial_sequence($5 OPERATOR(pg_catalog.||) "nspname" OPERATOR(pg_catalog.||) $6 OPERATOR(pg_catalog.||) "relname" OPERATOR(pg_catalog.||) $7, "attname")::regclass::oid as "seqId" FROM pg_catalog.pg_attribute attr JOIN pg_catalog.pg_class cls ON cls.oid OPERATOR(pg_catalog.=) attr.attrelid JOIN pg_catalog.pg_namespace nsp ON nsp.oid OPERATOR(pg_catalog.=) cls.relnamespace JOIN pg_catalog.pg_type t ON t.oid OPERATOR(pg_catalog.=) attr.atttypid WHERE attrelid IN ($8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26) AND attnum OPERATOR(pg_catalog.>) $27 AND attisdropped OPERATOR(pg_catalog.=) $28 ORDER BY attnum
```

### 29. `ListCoordinatorAssignmentsByEvent`

- **Role:** `od\_runtime`
- **Calls:** 16
- **Average time:** 3.4 ms
- **Total time:** 54.9 ms

```sql
-- name: ListCoordinatorAssignmentsByEvent :many SELECT eca.id, eca.user_id, u.email, u.name, eca.event_id, eca.created_at FROM event_coordinator_assignments eca JOIN users u ON u.id = eca.user_id WHERE eca.event_id = $1 ORDER BY eca.created_at ASC, eca.id ASC
```

### 30. `MarkStudentCardTokenVerified`

- **Role:** `od\_runtime`
- **Calls:** 213
- **Average time:** 0.2 ms
- **Total time:** 52.8 ms

```sql
-- name: MarkStudentCardTokenVerified :execrows UPDATE student_card_tokens SET verified_at = now(), verified_by_user_id = $1 WHERE version = $2 AND token_sha256 = $3
```

### 31. `SQL / System Query`

- **Role:** `neondb\_owner`
- **Calls:** 30
- **Average time:** 1.6 ms
- **Total time:** 46.9 ms

```sql
SELECT objid as oid, refobjid as "tableId", refobjsubid as "ordinality", deptype::text FROM pg_catalog.pg_depend WHERE refobjid IN ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)
```

### 32. `GetMarkStudentByID`

- **Role:** `od\_runtime`
- **Calls:** 1113
- **Average time:** 0 ms
- **Total time:** 41 ms

```sql
-- name: GetMarkStudentByID :one SELECT s.id, s.roll_no, s.name, b.code AS branch, s.section, s.batch FROM students s JOIN branches b ON b.id = s.branch_id WHERE s.id = $1
```

### 33. `CountStudentsForCardPopulation`

- **Role:** `od\_runtime`
- **Calls:** 247
- **Average time:** 0.2 ms
- **Total time:** 38.8 ms

```sql
-- name: CountStudentsForCardPopulation :one SELECT count(*) FROM students s WHERE ($1::uuid IS NULL OR s.branch_id = $1::uuid) AND ($2::text = $4 OR s.batch = $2::text) AND ($3::text = $5 OR s.section = $3::text)
```

### 34. `SQL / System Query`

- **Role:** `neondb\_owner`
- **Calls:** 35
- **Average time:** 1 ms
- **Total time:** 35 ms

```sql
SELECT adrelid AS "tableId", adnum AS "ordinality", pg_catalog.pg_get_expr(adbin, adrelid) AS "expression" FROM pg_catalog.pg_attrdef
```

### 35. `ListAssignedActiveEvents`

- **Role:** `od\_runtime`
- **Calls:** 274
- **Average time:** 0.1 ms
- **Total time:** 34.7 ms

```sql
-- name: ListAssignedActiveEvents :many SELECT e.id, e.name, e.event_date, e.end_date, e.status, e.archived_at, e.created_at, COALESCE( jsonb_agg( jsonb_build_object($2, b.id, $3, b.code, $4, b.name) ORDER BY b.code ASC, b.id ASC ) FILTER (WHERE b.id IS NOT NULL), $5::jsonb )::text AS branches_json FROM event_coordinator_assignments eca JOIN events e ON e.id = eca.event_id JOIN categories c ON c.event_id = e.id JOIN user_category_assignments uca ON uca.category_id = c.id AND uca.user_id = eca.user_id LEFT JOIN event_branches eb ON eb.event_id = e.id LEFT JOIN branches b ON b.id = eb.branch_id WHERE eca.user_id = $1 AND e.status = $6 GROUP BY e.id ORDER BY e.event_date DESC, e.created_at DESC, e.id DESC
```

### 36. `ListPublicDayODRecords`

- **Role:** `od\_runtime`
- **Calls:** 515
- **Average time:** 0.1 ms
- **Total time:** 33.6 ms

```sql
-- name: ListPublicDayODRecords :many SELECT s.roll_no, s.name AS student_name, r.slot, c.id AS category_id, c.name AS category_name, e.name AS event_name FROM od_records r JOIN students s ON s.id = r.student_id JOIN categories c ON c.id = r.category_id JOIN events e ON e.id = r.event_id WHERE r.od_date = $1 AND r.status = $5 AND ($2::integer IS NULL OR r.slot = $2) AND ($3::uuid IS NULL OR r.category_id = $3) ORDER BY s.roll_no ASC, r.slot ASC, c.name ASC LIMIT $4::bigint
```

### 37. `SQL / System Query`

- **Role:** `od\_migrator`
- **Calls:** 1
- **Average time:** 32.1 ms
- **Total time:** 32.1 ms

```sql
CREATE TABLE user_category_assignments ( user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT, category_id uuid NOT NULL REFERENCES categories(id) ON DELETE RESTRICT, created_by_user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT, created_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY (user_id, category_id) )
```

### 38. `SQL / System Query`

- **Role:** `od\_runtime`
- **Calls:** 5379
- **Average time:** 0 ms
- **Total time:** 28.4 ms

```sql
begin
```

### 39. `ListAttendanceRecords`

- **Role:** `od\_runtime`
- **Calls:** 191
- **Average time:** 0.1 ms
- **Total time:** 28.2 ms

```sql
-- name: ListAttendanceRecords :many SELECT r.id, r.student_id, s.roll_no, s.name AS student_name, b.code AS branch, s.section, s.batch, r.event_id, c.id AS category_id, c.name AS category_name, c.created_at AS category_created_at, r.slot, r.od_date, r.status, r.reason_note, r.capture_method, r.actor_user_id, u.email AS actor_email, u.name AS actor_name, r.actor_role, r.server_received_at FROM od_records r JOIN students s ON s.id = r.student_id JOIN branches b ON b.id = s.branch_id JOIN categories c ON c.id = r.category_id LEFT JOIN users u ON u.id = r.actor_user_id WHERE r.event_id = $1 AND ($2::uuid IS NULL OR r.category_id = $2) AND ($3::integer IS NULL OR r.slot = $3) AND ($4::date IS NULL OR r.od_date = $4) AND ($5::text IS NULL OR s.section = $5) AND ($6::uuid IS NULL OR r.actor_user_id = $6) AND ( -- BRANCH SCOPE. NULL means "no scoping", which is the admin view. A -- teacher passes their own id and sees only students in the branches they -- teach, matching the rule that already governs which students they may -- MARK — a teacher who cannot mark a student has no reason to read that -- student's record either. -- -- One query with an optional predicate rather than two, so the admin and -- teacher views can never drift in their ordering, their filters or their -- definition of "present". $7::uuid IS NULL OR EXISTS ( SELECT $10 FROM user_branches ub WHERE ub.user_id = $7 AND ub.branch_id = s.branch_id ) ) ORDER BY r.od_date DESC, r.slot ASC, r.server_received_at DESC, s.roll_no ASC, r.id ASC LIMIT $9::bigint OFFSET $8::bigint
```

### 40. `SQL / System Query`

- **Role:** `neondb\_owner`
- **Calls:** 4
- **Average time:** 6.4 ms
- **Total time:** 25.4 ms

```sql
SELECT oid, connamespace AS "schemaId", conrelid AS "tableId", conname AS "name", contype::text AS "type", pg_catalog.pg_get_constraintdef(oid) AS "definition", conindid AS "indexId", conkey AS "columnsOrdinals", confrelid AS "tableToId", confkey AS "columnsToOrdinals", confupdtype::text AS "onUpdate", confdeltype::text AS "onDelete" FROM pg_catalog.pg_constraint WHERE conrelid IN ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18) ORDER BY conrelid, contype, pg_catalog.lower(conname)
```

### 41. `SQL / System Query`

- **Role:** `neondb\_owner`
- **Calls:** 30
- **Average time:** 0.8 ms
- **Total time:** 25.2 ms

```sql
SELECT oid, adrelid as "tableId", adnum as "ordinality", pg_catalog.pg_get_expr(adbin, adrelid) as "expression" FROM pg_catalog.pg_attrdef WHERE adrelid IN ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)
```

### 42. `SQL / System Query`

- **Role:** `od\_runtime`
- **Calls:** 5066
- **Average time:** 0 ms
- **Total time:** 22.8 ms

```sql
commit
```

### 43. `SQL / System Query`

- **Role:** `neondb\_owner`
- **Calls:** 4
- **Average time:** 5.6 ms
- **Total time:** 22.6 ms

```sql
SELECT objid as oid, refobjid as "tableId", refobjsubid as "ordinality", deptype::text FROM pg_catalog.pg_depend WHERE refobjid IN ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
```

### 44. `ResolveStudentCardToken`

- **Role:** `od\_runtime`
- **Calls:** 425
- **Average time:** 0.1 ms
- **Total time:** 21.6 ms

```sql
-- name: ResolveStudentCardToken :one SELECT s.id, s.roll_no, s.name, b.code AS branch, s.section, s.batch FROM student_card_tokens t JOIN students s ON s.id = t.student_id JOIN branches b ON b.id = s.branch_id WHERE t.version = $1 AND t.token_sha256 = $2
```

### 45. `InsertDispute`

- **Role:** `od\_runtime`
- **Calls:** 14
- **Average time:** 1.5 ms
- **Total time:** 20.7 ms

```sql
-- name: InsertDispute :one INSERT INTO disputes ( student_id, event_id, category_id, od_date, slot, submitted_email, claimed_coordinator_name, student_note ) VALUES ( $1, $2, $3, $4, $5, $6, $7, $8 ) RETURNING id, created_at
```

### 46. `SQL / System Query`

- **Role:** `od\_migrator`
- **Calls:** 2
- **Average time:** 9.4 ms
- **Total time:** 18.8 ms

```sql
insert into students (roll_no, name, branch_id, section, batch) values ($1, $2, $3::uuid, $4, $5) on conflict (roll_no) do update set name=excluded.name, branch_id=excluded.branch_id, section=excluded.section, batch=excluded.batch, updated_at=now() where students.name is distinct from excluded.name or students.branch_id is distinct from excluded.branch_id or students.section is distinct from excluded.section or students.batch is distinct from excluded.batch returning id, (xmax = $6) as inserted
```

### 47. `ListPublicDayCategories`

- **Role:** `od\_runtime`
- **Calls:** 513
- **Average time:** 0 ms
- **Total time:** 18.8 ms

```sql
-- name: ListPublicDayCategories :many SELECT DISTINCT c.id, c.name FROM od_records r JOIN categories c ON c.id = r.category_id WHERE r.od_date = $1 AND r.status = $2 ORDER BY c.name ASC
```

### 48. `ListAdminPortalMarkEvents`

- **Role:** `od\_runtime`
- **Calls:** 168
- **Average time:** 0.1 ms
- **Total time:** 17.4 ms

```sql
-- name: ListAdminPortalMarkEvents :many SELECT e.id, e.name, e.event_date, e.end_date, e.status, e.archived_at, e.created_at, COALESCE( jsonb_agg( jsonb_build_object($1, b.id, $2, b.code, $3, b.name) ORDER BY b.code ASC, b.id ASC ) FILTER (WHERE b.id IS NOT NULL), $4::jsonb )::text AS branches_json FROM events e LEFT JOIN event_branches eb ON eb.event_id = e.id LEFT JOIN branches b ON b.id = eb.branch_id WHERE e.status = $5 GROUP BY e.id ORDER BY e.event_date ASC, e.created_at ASC, e.id ASC
```

### 49. `ListPublicStudentODRecords`

- **Role:** `od\_runtime`
- **Calls:** 293
- **Average time:** 0.1 ms
- **Total time:** 17.3 ms

```sql
-- name: ListPublicStudentODRecords :many SELECT r.id, r.slot, r.event_id, e.name AS event_name, e.event_date AS event_date, r.od_date AS od_date, c.id AS category_id, c.name AS category_name, u.name AS marked_by_name, r.actor_role AS marked_by_role, r.server_received_at FROM od_records r JOIN events e ON e.id = r.event_id JOIN categories c ON c.id = r.category_id LEFT JOIN users u ON u.id = r.actor_user_id WHERE r.student_id = $1 AND r.status = $3 AND c.visible_on_public_dashboard = $4 ORDER BY r.od_date DESC, r.slot ASC, r.server_received_at ASC, r.id ASC LIMIT $2::bigint
```

### 50. `ListEvents`

- **Role:** `od\_runtime`
- **Calls:** 156
- **Average time:** 0.1 ms
- **Total time:** 17.1 ms

```sql
-- name: ListEvents :many SELECT e.id, e.name, e.event_date, e.end_date, e.status, e.archived_at, e.created_at, COALESCE( jsonb_agg( jsonb_build_object($4, b.id, $5, b.code, $6, b.name) ORDER BY b.code ASC, b.id ASC ) FILTER (WHERE b.id IS NOT NULL), $7::jsonb )::text AS branches_json FROM events e LEFT JOIN event_branches eb ON eb.event_id = e.id LEFT JOIN branches b ON b.id = eb.branch_id WHERE ($1::text = $8 OR e.status = $1::text) GROUP BY e.id ORDER BY e.event_date DESC, e.created_at DESC, e.id DESC LIMIT $3::bigint OFFSET $2::bigint
```

### 51. `GetStudentByRollNo`

- **Role:** `od\_runtime`
- **Calls:** 14
- **Average time:** 1.2 ms
- **Total time:** 16.5 ms

```sql
-- name: GetStudentByRollNo :one SELECT s.id, s.roll_no, s.name, b.code AS branch, s.section, s.batch FROM students s JOIN branches b ON b.id = s.branch_id WHERE s.roll_no = $1 LIMIT $2
```

### 52. `SQL / System Query`

- **Role:** `neondb\_owner`
- **Calls:** 4
- **Average time:** 4 ms
- **Total time:** 15.8 ms

```sql
SELECT attrelid AS "tableId", relkind::text AS "kind", attname AS "name", attnum AS "ordinality", attnotnull AS "notNull", CASE WHEN attndims > $1 THEN attndims WHEN t.typcategory = $2 THEN $3 -- If it's an array type, default to 1 dimension ELSE $4 END as "dimensions", atttypid as "typeId", attgenerated::text as "generatedType", attidentity::text as "identityType", pg_catalog.format_type(atttypid, atttypmod) as "type", pg_catalog.pg_get_serial_sequence($5 OPERATOR(pg_catalog.||) "nspname" OPERATOR(pg_catalog.||) $6 OPERATOR(pg_catalog.||) "relname" OPERATOR(pg_catalog.||) $7, "attname")::regclass::oid as "seqId" FROM pg_catalog.pg_attribute attr JOIN pg_catalog.pg_class cls ON cls.oid OPERATOR(pg_catalog.=) attr.attrelid JOIN pg_catalog.pg_namespace nsp ON nsp.oid OPERATOR(pg_catalog.=) cls.relnamespace JOIN pg_catalog.pg_type t ON t.oid OPERATOR(pg_catalog.=) attr.atttypid WHERE attrelid IN ($8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25) AND attnum OPERATOR(pg_catalog.>) $26 AND attisdropped OPERATOR(pg_catalog.=) $27 ORDER BY attnum
```

### 53. `GetMarkCategoryForEvent`

- **Role:** `od\_runtime`
- **Calls:** 627
- **Average time:** 0 ms
- **Total time:** 15.8 ms

```sql
-- name: GetMarkCategoryForEvent :one SELECT id, event_id FROM categories WHERE id = $1 AND event_id = $2
```

### 54. `InsertEventCoordinatorAssignment`

- **Role:** `od\_runtime`
- **Calls:** 11
- **Average time:** 1.4 ms
- **Total time:** 15.5 ms

```sql
-- name: InsertEventCoordinatorAssignment :one INSERT INTO event_coordinator_assignments (user_id, event_id, created_by_user_id) VALUES ($1, $2, $3) RETURNING id, user_id, event_id, created_at
```

### 55. `CountAttendanceRecords`

- **Role:** `od\_runtime`
- **Calls:** 191
- **Average time:** 0.1 ms
- **Total time:** 13.1 ms

```sql
-- name: CountAttendanceRecords :one SELECT count(*)::bigint FROM od_records r JOIN students s ON s.id = r.student_id WHERE r.event_id = $1 AND ($2::uuid IS NULL OR r.category_id = $2) AND ($3::integer IS NULL OR r.slot = $3) AND ($4::date IS NULL OR r.od_date = $4) AND ($5::text IS NULL OR s.section = $5) AND ($6::uuid IS NULL OR r.actor_user_id = $6) AND ( -- BRANCH SCOPE. NULL means "no scoping", which is the admin view. A -- teacher passes their own id and sees only students in the branches they -- teach, matching the rule that already governs which students they may -- MARK — a teacher who cannot mark a student has no reason to read that -- student's record either. -- -- One query with an optional predicate rather than two, so the admin and -- teacher views can never drift in their ordering, their filters or their -- definition of "present". $7::uuid IS NULL OR EXISTS ( SELECT $8 FROM user_branches ub WHERE ub.user_id = $7 AND ub.branch_id = s.branch_id ) )
```

### 56. `ListCoordinatorAssignmentsByEvent`

- **Role:** `od\_runtime`
- **Calls:** 53
- **Average time:** 0.2 ms
- **Total time:** 12.8 ms

```sql
-- name: ListCoordinatorAssignmentsByEvent :many SELECT eca.id, eca.user_id, u.email, u.name, eca.event_id, COALESCE( jsonb_agg( jsonb_build_object( $2, c.id, $3, c.event_id, $4, c.name, $5, c.visible_on_public_dashboard, $6, c.created_at ) ORDER BY c.name ASC, c.id ASC ) FILTER (WHERE c.id IS NOT NULL), $7::jsonb )::text AS categories_json, eca.created_at FROM event_coordinator_assignments eca JOIN users u ON u.id = eca.user_id LEFT JOIN user_category_assignments uca ON uca.user_id = eca.user_id LEFT JOIN categories c ON c.id = uca.category_id AND c.event_id = eca.event_id WHERE eca.event_id = $1 GROUP BY eca.id, u.id ORDER BY eca.created_at ASC, eca.id ASC
```

### 57. `ListBranches`

- **Role:** `od\_runtime`
- **Calls:** 341
- **Average time:** 0 ms
- **Total time:** 12.5 ms

```sql
-- name: ListBranches :many SELECT id, code, name FROM branches ORDER BY code ASC, id ASC
```

### 58. `SQL / System Query`

- **Role:** `neondb\_owner`
- **Calls:** 4
- **Average time:** 2.6 ms
- **Total time:** 10.3 ms

```sql
SELECT oid, adrelid as "tableId", adnum as "ordinality", pg_catalog.pg_get_expr(adbin, adrelid) as "expression" FROM pg_catalog.pg_attrdef WHERE adrelid IN ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
```

### 59. `SQL / System Query`

- **Role:** `neondb\_owner`
- **Calls:** 672
- **Average time:** 0 ms
- **Total time:** 10 ms

```sql
DISCARD TEMP
```

### 60. `ListAssignedActiveEvents`

- **Role:** `od\_runtime`
- **Calls:** 69
- **Average time:** 0.1 ms
- **Total time:** 10 ms

```sql
-- name: ListAssignedActiveEvents :many SELECT e.id, e.name, e.event_date, e.end_date, e.status, e.archived_at, e.created_at, COALESCE( jsonb_agg( jsonb_build_object($2, b.id, $3, b.code, $4, b.name) ORDER BY b.code ASC, b.id ASC ) FILTER (WHERE b.id IS NOT NULL), $5::jsonb )::text AS branches_json FROM event_coordinator_assignments eca JOIN events e ON e.id = eca.event_id LEFT JOIN event_branches eb ON eb.event_id = e.id LEFT JOIN branches b ON b.id = eb.branch_id WHERE eca.user_id = $1 AND e.status = $6 GROUP BY e.id ORDER BY e.event_date DESC, e.created_at DESC, e.id DESC
```

### 61. `SQL / System Query`

- **Role:** `od\_migrator`
- **Calls:** 1
- **Average time:** 9.5 ms
- **Total time:** 9.5 ms

```sql
insert into student_roster_imports ( source_filename, total_rows, inserted_rows, updated_rows, unchanged_rows, invalid_rows, database_count, actor_user_id, actor_email ) values ($1, $2, $3, $4, $5, $8, $6, $9, $7) returning id
```

### 62. `SQL / System Query`

- **Role:** `od\_migrator`
- **Calls:** 1
- **Average time:** 9.1 ms
- **Total time:** 9.1 ms

```sql
ALTER TABLE event_coordinator_assignments ADD COLUMN fallback_credit_limit integer NOT NULL DEFAULT 5 CHECK (fallback_credit_limit >= 0)
```

### 63. `SQL / System Query`

- **Role:** `od\_migrator`
- **Calls:** 1
- **Average time:** 8.9 ms
- **Total time:** 8.9 ms

```sql
WITH admin_user AS (SELECT id FROM users WHERE role=$1 ORDER BY created_at ASC, email ASC LIMIT $2), hidden AS (UPDATE categories SET visible_on_public_dashboard=$3 WHERE name ILIKE $4 RETURNING id), inserted AS (INSERT INTO user_category_assignments (user_id, category_id, created_by_user_id) SELECT DISTINCT u.id, c.id, admin_user.id FROM od_records r JOIN users u ON u.id=r.actor_user_id JOIN categories c ON c.id=r.category_id CROSS JOIN admin_user WHERE r.actor_role IN ($5,$6) AND u.role IN ($7,$8) ON CONFLICT (user_id, category_id) DO NOTHING RETURNING user_id, category_id) SELECT (SELECT count(*)::text FROM hidden) AS hidden_categories, (SELECT count(*)::text FROM inserted) AS inserted_assignments
```

### 64. `SQL / System Query`

- **Role:** `od\_migrator`
- **Calls:** 1
- **Average time:** 8.9 ms
- **Total time:** 8.9 ms

```sql
ALTER TABLE categories ADD COLUMN visible_on_public_dashboard boolean NOT NULL DEFAULT true
```

### 65. `SQL / System Query`

- **Role:** `od\_migrator`
- **Calls:** 1
- **Average time:** 8.8 ms
- **Total time:** 8.8 ms

```sql
COMMENT ON COLUMN event_coordinator_assignments.fallback_credit_limit IS 'Camera/search fallback credit limit for this coordinator assignment. NFC is unlimited.'
```

### 66. `SQL / System Query`

- **Role:** `neondb\_owner`
- **Calls:** 672
- **Average time:** 0 ms
- **Total time:** 8.5 ms

```sql
RESET ALL
```

### 67. `SQL / System Query`

- **Role:** `neondb\_owner`
- **Calls:** 35
- **Average time:** 0.2 ms
- **Total time:** 8.5 ms

```sql
SELECT oid, nspname as name FROM pg_catalog.pg_namespace WHERE pg_catalog.has_schema_privilege(oid, $1) ORDER BY pg_catalog.lower(nspname)
```

### 68. `ListTeacherPortalMarkEvents`

- **Role:** `od\_runtime`
- **Calls:** 46
- **Average time:** 0.2 ms
- **Total time:** 8.2 ms

```sql
-- name: ListTeacherPortalMarkEvents :many SELECT e.id, e.name, e.event_date, e.end_date, e.status, e.archived_at, e.created_at, COALESCE( jsonb_agg( jsonb_build_object($2, b.id, $3, b.code, $4, b.name) ORDER BY b.code ASC, b.id ASC ) FILTER (WHERE b.id IS NOT NULL), $5::jsonb )::text AS branches_json FROM events e LEFT JOIN event_branches eb ON eb.event_id = e.id LEFT JOIN branches b ON b.id = eb.branch_id WHERE e.status = $6 AND EXISTS ( SELECT $7 FROM event_branches markable_eb JOIN user_branches ub ON ub.branch_id = markable_eb.branch_id WHERE markable_eb.event_id = e.id AND ub.user_id = $1 ) GROUP BY e.id ORDER BY e.event_date ASC, e.created_at ASC, e.id ASC
```

### 69. `TeacherCanSearchMark`

- **Role:** `od\_runtime`
- **Calls:** 9
- **Average time:** 0.9 ms
- **Total time:** 8 ms

```sql
-- name: TeacherCanSearchMark :one SELECT EXISTS ( SELECT $5 FROM students s JOIN user_branches ub ON ub.branch_id = s.branch_id AND ub.user_id = $1 JOIN event_branches eb ON eb.branch_id = s.branch_id AND eb.event_id = $2 JOIN events e ON e.id = eb.event_id AND e.status = $6 JOIN categories c ON c.id = $3 AND c.event_id = e.id JOIN user_category_assignments uca ON uca.category_id = c.id AND uca.user_id = $1 WHERE s.id = $4 )::boolean
```

### 70. `SQL / System Query`

- **Role:** `neondb\_owner`
- **Calls:** 4
- **Average time:** 1.7 ms
- **Total time:** 7 ms

```sql
SELECT pg_class.oid, nspname as "schema", relname AS "name", am.amname AS "accessMethod", reloptions AS "with", pg_catalog.row_to_json(metadata.*) as "metadata" FROM pg_catalog.pg_class JOIN pg_catalog.pg_am am ON am.oid OPERATOR(pg_catalog.=) pg_class.relam JOIN pg_catalog.pg_namespace nsp ON nsp.oid OPERATOR(pg_catalog.=) pg_class.relnamespace JOIN LATERAL ( SELECT pg_catalog.pg_get_expr(indexprs, indrelid) AS "expression", pg_catalog.pg_get_expr(indpred, indrelid) AS "where", indrelid::int AS "tableId", indkey::int[] as "columnOrdinals", indoption::int[] as "options", indisunique as "isUnique", indisprimary as "isPrimary", array( SELECT pg_catalog.json_build_object( $1, opclass.oid, $2, pg_am.amname, $3, pg_opclass.opcname, $4, pg_opclass.opcdefault ) FROM pg_catalog.unnest(indclass) WITH ORDINALITY AS opclass(oid, ordinality) JOIN pg_catalog.pg_opclass ON opclass.oid OPERATOR(pg_catalog.=) pg_opclass.oid JOIN pg_catalog.pg_am ON pg_opclass.opcmethod OPERATOR(pg_catalog.=) pg_am.oid ORDER BY opclass.ordinality ) as "opclasses" FROM pg_catalog.pg_index WHERE pg_index.indexrelid OPERATOR(pg_catalog.=) pg_class.oid ) metadata ON $5 WHERE relkind OPERATOR(pg_catalog.=) ANY (ARRAY[$6, $7]) AND metadata."tableId" IN ($8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25) ORDER BY pg_catalog.lower(nspname), pg_catalog.lower(relname)
```

### 71. `AssignUserCategory`

- **Role:** `od\_runtime`
- **Calls:** 29
- **Average time:** 0.2 ms
- **Total time:** 6.8 ms

```sql
-- name: AssignUserCategory :one INSERT INTO user_category_assignments (user_id, category_id, created_by_user_id) VALUES ($1, $2, $3) ON CONFLICT (user_id, category_id) DO NOTHING RETURNING user_id, category_id, created_by_user_id, created_at
```

### 72. `ListAdminPortalMarkCategories`

- **Role:** `od\_runtime`
- **Calls:** 115
- **Average time:** 0.1 ms
- **Total time:** 5.9 ms

```sql
-- name: ListAdminPortalMarkCategories :many SELECT c.id, c.event_id, c.name, c.visible_on_public_dashboard, c.created_at FROM categories c JOIN events e ON e.id = c.event_id WHERE c.event_id = $1 AND e.status = $2 ORDER BY c.name ASC, c.id ASC
```

### 73. `SQL / System Query`

- **Role:** `neondb\_owner`
- **Calls:** 34
- **Average time:** 0.2 ms
- **Total time:** 5.7 ms

```sql
SELECT nspname as "schema", relname as "name", seqrelid as "oid", seqstart as "startWith", seqmin as "minValue", seqmax as "maxValue", seqincrement as "incrementBy", seqcycle as "cycle", seqcache as "cacheSize" FROM pg_catalog.pg_sequence JOIN pg_catalog.pg_class ON pg_sequence.seqrelid OPERATOR(pg_catalog.=) pg_class.oid JOIN pg_catalog.pg_namespace ON pg_namespace.oid OPERATOR(pg_catalog.=) pg_class.relnamespace WHERE nspname IN ($1) ORDER BY pg_catalog.lower(nspname), pg_catalog.lower(relname)
```

### 74. `SQL / System Query`

- **Role:** `neondb\_owner`
- **Calls:** 34
- **Average time:** 0.2 ms
- **Total time:** 5.6 ms

```sql
SELECT pg_class.oid, nspname as "schema", relname AS "name", relkind::text AS "kind", relam as "accessMethod", reloptions::text[] as "options", reltablespace as "tablespaceid", relrowsecurity AS "rlsEnabled", CASE WHEN relkind OPERATOR(pg_catalog.=) $1 OR relkind OPERATOR(pg_catalog.=) $2 THEN pg_catalog.pg_get_viewdef(pg_class.oid, $3) ELSE $4 END as "definition" FROM pg_catalog.pg_class JOIN pg_catalog.pg_namespace ON pg_namespace.oid OPERATOR(pg_catalog.=) relnamespace WHERE relkind IN ($5, $6, $7, $8) AND nspname IN ($9) ORDER BY pg_catalog.lower(nspname), pg_catalog.lower(relname)
```

### 75. `SQL / System Query`

- **Role:** `od\_migrator`
- **Calls:** 2
- **Average time:** 2.7 ms
- **Total time:** 5.3 ms

```sql
INSERT INTO goose_db_version (version_id, is_applied) VALUES ($1, $2)
```

### 76. `ListCaptureCreditUsageByEvent`

- **Role:** `od\_runtime`
- **Calls:** 60
- **Average time:** 0.1 ms
- **Total time:** 5.3 ms

```sql
-- name: ListCaptureCreditUsageByEvent :many WITH effective AS ( SELECT COALESCE(cfg.credit_limit, $3)::integer AS credit_limit FROM events e LEFT JOIN event_capture_credit_config cfg ON cfg.event_id = e.id WHERE e.id = $2 ) SELECT eca.event_id, eca.user_id, u.email, u.name, effective.credit_limit, COALESCE(usage.consumed, $4)::integer AS consumed, GREATEST(effective.credit_limit - COALESCE(usage.consumed, $5), $6)::integer AS remaining, (COALESCE(usage.consumed, $7) >= effective.credit_limit)::boolean AS exhausted FROM event_coordinator_assignments eca JOIN users u ON u.id = eca.user_id CROSS JOIN effective LEFT JOIN event_capture_credit_usage usage ON usage.event_id = eca.event_id AND usage.user_id = eca.user_id AND usage.usage_date = $1 WHERE eca.event_id = $2 ORDER BY eca.created_at ASC, eca.id ASC
```

### 77. `CreateODRecordFromApprovedDispute`

- **Role:** `od\_runtime`
- **Calls:** 5
- **Average time:** 1 ms
- **Total time:** 5 ms

```sql
-- name: CreateODRecordFromApprovedDispute :one INSERT INTO od_records ( student_id, event_id, category_id, slot, od_date, status, capture_method, actor_user_id, actor_role, reason_note ) SELECT d.student_id, d.event_id, d.category_id, d.slot, d.od_date, $5, $6, $1, $2::text, $3::text FROM disputes d WHERE d.id = $4 ON CONFLICT (student_id, event_id, od_date, slot) WHERE status = $7 DO NOTHING RETURNING id
```

### 78. `AdminCanSearchMark`

- **Role:** `od\_runtime`
- **Calls:** 159
- **Average time:** 0 ms
- **Total time:** 4.9 ms

```sql
-- name: AdminCanSearchMark :one SELECT EXISTS ( SELECT $3 FROM events e JOIN categories c ON c.event_id = e.id AND c.id = $1 WHERE e.id = $2 AND e.status = $4 )::boolean
```

### 79. `SQL / System Query`

- **Role:** `neondb\_owner`
- **Calls:** 672
- **Average time:** 0 ms
- **Total time:** 4.5 ms

```sql
SET SESSION AUTHORIZATION DEFAULT
```

### 80. `SQL / System Query`

- **Role:** `neondb\_owner`
- **Calls:** 34
- **Average time:** 0.1 ms
- **Total time:** 4.3 ms

```sql
SELECT pg_type.oid as "oid", typname as "name", nspname as "schema", pg_type.typarray as "arrayTypeId", pg_enum.enumsortorder AS "ordinality", pg_enum.enumlabel AS "value" FROM pg_catalog.pg_type JOIN pg_catalog.pg_enum ON pg_enum.enumtypid OPERATOR(pg_catalog.=) pg_type.oid JOIN pg_catalog.pg_namespace ON pg_namespace.oid OPERATOR(pg_catalog.=) pg_type.typnamespace WHERE pg_type.typtype OPERATOR(pg_catalog.=) $1 AND nspname IN ($2) ORDER BY pg_type.oid, pg_enum.enumsortorder
```

### 81. `SQL / System Query`

- **Role:** `od\_migrator`
- **Calls:** 1
- **Average time:** 4.3 ms
- **Total time:** 4.3 ms

```sql
WITH old_events AS (SELECT id FROM events WHERE name IN ($1,$2)), kept_events AS (SELECT id FROM events WHERE name=$3), old_categories AS (SELECT id FROM categories WHERE event_id IN (SELECT id FROM old_events)), deleted_od AS (DELETE FROM od_records RETURNING id), reset_kept_usage AS (UPDATE event_capture_credit_usage SET consumed=$4, updated_at=now() WHERE event_id IN (SELECT id FROM kept_events) RETURNING event_id, user_id), deleted_old_usage AS (DELETE FROM event_capture_credit_usage WHERE event_id IN (SELECT id FROM old_events) RETURNING event_id, user_id), deleted_old_config AS (DELETE FROM event_capture_credit_config WHERE event_id IN (SELECT id FROM old_events) RETURNING event_id), deleted_old_coordinators AS (DELETE FROM event_coordinator_assignments WHERE event_id IN (SELECT id FROM old_events) RETURNING id), deleted_old_assignments AS (DELETE FROM user_category_assignments WHERE category_id IN (SELECT id FROM old_categories) RETURNING user_id, category_id), deleted_old_disputes AS (DELETE FROM disputes WHERE event_id IN (SELECT id FROM old_events) RETURNING id), deleted_old_branches AS (DELETE FROM event_branches WHERE event_id IN (SELECT id FROM old_events) RETURNING event_id, branch_id), deleted_old_categories AS (DELETE FROM categories WHERE event_id IN (SELECT id FROM old_events) RETURNING id), deleted_old_events AS (DELETE FROM events WHERE id IN (SELECT id FROM old_events) RETURNING id, name) SELECT (SELECT COUNT(*) FROM deleted_od) AS od_records_deleted, (SELECT COUNT(*) FROM reset_kept_usage) AS kept_credit_rows_reset, (SELECT COUNT(*) FROM deleted_old_usage) AS old_credit_rows_deleted, (SELECT COUNT(*) FROM deleted_old_config) AS old_credit_configs_deleted, (SELECT COUNT(*) FROM deleted_old_coordinators) AS old_coordinators_deleted, (SELECT COUNT(*) FROM deleted_old_assignments) AS old_category_assignments_deleted, (SELECT COUNT(*) FROM deleted_old_disputes) AS old_disputes_deleted, (SELECT COUNT(*) FROM deleted_old_branches) AS old_branch_links_deleted, (SELECT COUNT(*) FROM deleted_old_categories) AS old_categories_deleted, (SELECT COUNT(*) FROM deleted_old_events) AS old_events_deleted
```

### 82. `SQL / System Query`

- **Role:** `neondb\_owner`
- **Calls:** 672
- **Average time:** 0 ms
- **Total time:** 4.3 ms

```sql
CLOSE ALL
```

### 83. `GetEventMutationStatus`

- **Role:** `od\_runtime`
- **Calls:** 235
- **Average time:** 0 ms
- **Total time:** 4.2 ms

```sql
-- name: GetEventMutationStatus :one SELECT status FROM events WHERE id = $1
```

### 84. `ResetCaptureCreditUsage`

- **Role:** `od\_runtime`
- **Calls:** 5
- **Average time:** 0.8 ms
- **Total time:** 3.9 ms

```sql
-- name: ResetCaptureCreditUsage :one WITH assignment AS ( SELECT eca.event_id, eca.user_id FROM event_coordinator_assignments eca WHERE eca.event_id = $1 AND eca.user_id = $2 FOR UPDATE ), previous AS ( SELECT COALESCE(usage.consumed, $4)::integer AS previous_consumed FROM assignment LEFT JOIN event_capture_credit_usage usage ON usage.event_id = assignment.event_id AND usage.user_id = assignment.user_id AND usage.usage_date = $3 ), reset AS ( INSERT INTO event_capture_credit_usage (event_id, user_id, usage_date, consumed, updated_at) SELECT assignment.event_id, assignment.user_id, $3, $5, now() FROM assignment ON CONFLICT (event_id, user_id, usage_date) DO UPDATE SET consumed = $6, updated_at = now() RETURNING event_id, user_id, consumed, updated_at ) SELECT reset.event_id, reset.user_id, reset.consumed, reset.updated_at, previous.previous_consumed FROM reset, previous
```

### 85. `SQL / System Query`

- **Role:** `od\_migrator`
- **Calls:** 2
- **Average time:** 1.8 ms
- **Total time:** 3.5 ms

```sql
select id from branches where lower(code)=lower($1) limit $2
```

### 86. `UpsertCaptureCreditConfig`

- **Role:** `od\_runtime`
- **Calls:** 1
- **Average time:** 3.5 ms
- **Total time:** 3.5 ms

```sql
-- name: UpsertCaptureCreditConfig :one WITH locked_event AS ( SELECT id FROM events WHERE id = $1 FOR UPDATE ), previous AS ( SELECT COALESCE(cfg.credit_limit, $4)::integer AS previous_limit FROM locked_event LEFT JOIN event_capture_credit_config cfg ON cfg.event_id = locked_event.id ), updated AS ( INSERT INTO event_capture_credit_config (event_id, credit_limit, updated_by_user_id, updated_at) SELECT locked_event.id, $2, $3, now() FROM locked_event ON CONFLICT (event_id) DO UPDATE SET credit_limit = EXCLUDED.credit_limit, updated_by_user_id = EXCLUDED.updated_by_user_id, updated_at = now() RETURNING event_id, credit_limit, updated_by_user_id, updated_at ) SELECT updated.event_id, updated.credit_limit, updated.updated_by_user_id, updated.updated_at, previous.previous_limit FROM updated, previous
```

### 87. `CountDisputesForAdmin`

- **Role:** `od\_runtime`
- **Calls:** 19
- **Average time:** 0.2 ms
- **Total time:** 3.4 ms

```sql
-- name: CountDisputesForAdmin :one SELECT count(*)::bigint FROM disputes d WHERE ($1::text = $2 OR d.status = $1::text)
```

### 88. `SetCategoryPublicVisibility`

- **Role:** `od\_runtime`
- **Calls:** 3
- **Average time:** 1.1 ms
- **Total time:** 3.3 ms

```sql
-- name: SetCategoryPublicVisibility :one UPDATE categories SET visible_on_public_dashboard = $1, updated_at = now() WHERE id = $2 RETURNING id, event_id, name, visible_on_public_dashboard, created_at
```

### 89. `SQL / System Query`

- **Role:** `neondb\_owner`
- **Calls:** 45
- **Average time:** 0.1 ms
- **Total time:** 3.1 ms

```sql
select "id", "supabase_subject", "email", "name", "role", "created_at", "updated_at", "last_login_at" from "users" order by "users"."id" limit $1
```

### 90. `SQL / System Query`

- **Role:** `neondb\_owner`
- **Calls:** 672
- **Average time:** 0 ms
- **Total time:** 3 ms

```sql
SELECT pg_advisory_unlock_all()
```

### 91. `CountEvents`

- **Role:** `od\_runtime`
- **Calls:** 157
- **Average time:** 0 ms
- **Total time:** 2.8 ms

```sql
-- name: CountEvents :one SELECT count(*) FROM events WHERE ($1::text = $2 OR status = $1::text)
```

### 92. `ListTeachers`

- **Role:** `od\_runtime`
- **Calls:** 14
- **Average time:** 0.2 ms
- **Total time:** 2.7 ms

```sql
-- name: ListTeachers :many SELECT u.id, u.email, u.name, u.created_at, COALESCE( jsonb_agg( jsonb_build_object($3, b.id, $4, b.code, $5, b.name) ORDER BY b.code ASC, b.id ASC ) FILTER (WHERE b.id IS NOT NULL), $6::jsonb )::text AS branches_json, COALESCE( ( SELECT jsonb_agg( jsonb_build_object( $7, c.id, $8, c.event_id, $9, c.name, $10, c.visible_on_public_dashboard, $11, c.created_at ) ORDER BY c.name ASC, c.id ASC ) FROM user_category_assignments uca JOIN categories c ON c.id = uca.category_id WHERE uca.user_id = u.id ), $12::jsonb )::text AS categories_json FROM users u LEFT JOIN user_branches ub ON ub.user_id = u.id LEFT JOIN branches b ON b.id = ub.branch_id WHERE u.role = $13 GROUP BY u.id ORDER BY u.email ASC, u.id ASC LIMIT $2::bigint OFFSET $1::bigint
```

### 93. `ListPublicEvents`

- **Role:** `od\_runtime`
- **Calls:** 28
- **Average time:** 0.1 ms
- **Total time:** 2.6 ms

```sql
-- name: ListPublicEvents :many SELECT e.id, e.name, e.event_date, e.end_date, COALESCE( jsonb_agg( jsonb_build_object($2, c.id, $3, c.name) ORDER BY c.name ASC, c.id ASC ) FILTER (WHERE c.id IS NOT NULL), $4::jsonb )::text AS categories_json FROM events e LEFT JOIN categories c ON c.event_id = e.id AND c.visible_on_public_dashboard = $5 GROUP BY e.id ORDER BY e.event_date DESC, e.created_at DESC, e.id DESC LIMIT $1::bigint
```

### 94. `ResolveDispute`

- **Role:** `od\_runtime`
- **Calls:** 8
- **Average time:** 0.3 ms
- **Total time:** 2.4 ms

```sql
-- name: ResolveDispute :execrows UPDATE disputes SET status = $6, outcome = $1::text, resolution_note = $2, resolved_by_user_id = $3, resolved_at = $4 WHERE id = $5 AND status = $7
```

### 95. `SQL / System Query`

- **Role:** `od\_migrator`
- **Calls:** 2
- **Average time:** 1.2 ms
- **Total time:** 2.3 ms

```sql
select id, name, branch_id::text as branch_id, section, batch from students where roll_no=$1
```

### 96. `SQL / System Query`

- **Role:** `neondb\_owner`
- **Calls:** 35
- **Average time:** 0.1 ms
- **Total time:** 2.3 ms

```sql
SELECT oid, amname as name FROM pg_catalog.pg_am WHERE amtype OPERATOR(pg_catalog.=) $1 ORDER BY pg_catalog.lower(amname)
```

### 97. `ListTeachers`

- **Role:** `od\_runtime`
- **Calls:** 7
- **Average time:** 0.3 ms
- **Total time:** 2 ms

```sql
-- name: ListTeachers :many SELECT u.id, u.email, u.name, u.created_at, COALESCE( jsonb_agg( jsonb_build_object($3, b.id, $4, b.code, $5, b.name) ORDER BY b.code ASC, b.id ASC ) FILTER (WHERE b.id IS NOT NULL), $6::jsonb )::text AS branches_json FROM users u LEFT JOIN user_branches ub ON ub.user_id = u.id LEFT JOIN branches b ON b.id = ub.branch_id WHERE u.role = $7 GROUP BY u.id ORDER BY u.email ASC, u.id ASC LIMIT $2::bigint OFFSET $1::bigint
```

### 98. `SQL / System Query`

- **Role:** `neondb\_owner`
- **Calls:** 15
- **Average time:** 0.1 ms
- **Total time:** 1.9 ms

```sql
update "users" set "role" = $1 where "id" = $2
```

### 99. `ListTeacherPortalMarkCategories`

- **Role:** `od\_runtime`
- **Calls:** 17
- **Average time:** 0.1 ms
- **Total time:** 1.9 ms

```sql
-- name: ListTeacherPortalMarkCategories :many SELECT c.id, c.event_id, c.name, c.visible_on_public_dashboard, c.created_at FROM categories c JOIN events e ON e.id = c.event_id WHERE c.event_id = $1 AND e.status = $3 AND EXISTS ( SELECT $4 FROM event_branches eb JOIN user_branches ub ON ub.branch_id = eb.branch_id WHERE eb.event_id = c.event_id AND ub.user_id = $2 ) AND EXISTS ( SELECT $5 FROM user_category_assignments uca WHERE uca.user_id = $2 AND uca.category_id = c.id ) ORDER BY c.name ASC, c.id ASC
```

### 100. `SQL / System Query`

- **Role:** `od\_migrator`
- **Calls:** 9
- **Average time:** 0.2 ms
- **Total time:** 1.8 ms

```sql
SELECT version_id, is_applied from goose_db_version ORDER BY id DESC
```

## Source Page Details

- **Owner:** Shruhath Reddy
- **Project:** Amrita-OD
- **Branch:** production
- **Database:** neondb
- **Monitoring state:** All OK
- **Items per page:** 100
- **Displayed range:** 1–100 of 100
