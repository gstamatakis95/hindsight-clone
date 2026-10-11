-- EventAppend stores one lifecycle event; a NULL at takes the database's now().
-- name: EventAppend :exec
INSERT INTO lifecycle_events (tenant_id, bank_id, seq, type, at, operation_id, subject_id)
VALUES (
    @tenant_id, @bank_id, @seq, @type, COALESCE(sqlc.narg(at)::timestamptz, now()), @operation_id, @subject_id
);

-- EventList pages a bank's events after after_seq in one statement (one snapshot). The page ends at the seq of
-- the lim-th matching event (lim <= 0: no limit) or, with fewer matches, at the highest seq after after_seq
-- (after_seq itself when there is none), so a seq group is never split. Every row carries that bound; with no
-- matching event the single row has NULL event columns.
-- name: EventList :many
WITH p AS (
    SELECT @tenant_id::text AS tenant_id, @bank_id::text AS bank_id, @after_seq::bigint AS after_seq,
           @lim::int AS lim, @types::text[] AS types
), bound AS (
    SELECT COALESCE(
        (SELECT m.seq FROM lifecycle_events m, p
         WHERE m.tenant_id = p.tenant_id AND m.bank_id = p.bank_id AND m.seq > p.after_seq AND p.lim > 0
           AND (cardinality(p.types) = 0 OR m.type = ANY(p.types))
         ORDER BY m.seq, m.id OFFSET (SELECT GREATEST(lim - 1, 0) FROM p) LIMIT 1),
        (SELECT max(a.seq) FROM lifecycle_events a, p
         WHERE a.tenant_id = p.tenant_id AND a.bank_id = p.bank_id AND a.seq > p.after_seq),
        (SELECT after_seq FROM p)
    )::bigint AS seq
)
SELECT bound.seq AS bound_seq, e.id AS event_id, e.seq AS event_seq, e.type AS event_type, e.at AS event_at,
       e.operation_id AS event_operation_id, e.subject_id AS event_subject_id
FROM p CROSS JOIN bound
LEFT JOIN lifecycle_events e
  ON e.tenant_id = p.tenant_id AND e.bank_id = p.bank_id AND e.seq > p.after_seq AND e.seq <= bound.seq
 AND (cardinality(p.types) = 0 OR e.type = ANY(p.types))
ORDER BY e.seq, e.id;

-- EventPrune deletes events written before the cutoff.
-- name: EventPrune :execrows
DELETE FROM lifecycle_events WHERE at < @before;
