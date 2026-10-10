-- BankDeleteUnitEntities deletes up to lim unit_entities rows of the bank (via memory_units).
-- name: BankDeleteUnitEntities :execrows
DELETE FROM unit_entities WHERE ctid = ANY(ARRAY(
    SELECT c.ctid FROM unit_entities c JOIN memory_units p ON p.id = c.unit_id
    WHERE p.tenant_id = @tenant_id AND p.bank_id = @bank_id
    LIMIT @lim::int
));

-- BankDeleteCooccurrences deletes up to lim entity_cooccurrences rows of the bank (via entities).
-- name: BankDeleteCooccurrences :execrows
DELETE FROM entity_cooccurrences WHERE ctid = ANY(ARRAY(
    SELECT c.ctid FROM entity_cooccurrences c JOIN entities p ON p.id = c.entity_a
    WHERE p.tenant_id = @tenant_id AND p.bank_id = @bank_id
    LIMIT @lim::int
));

-- BankDeleteLinks deletes up to lim memory_links rows of the bank (via memory_units).
-- name: BankDeleteLinks :execrows
DELETE FROM memory_links WHERE ctid = ANY(ARRAY(
    SELECT c.ctid FROM memory_links c JOIN memory_units p ON p.id = c.from_unit_id
    WHERE p.tenant_id = @tenant_id AND p.bank_id = @bank_id
    LIMIT @lim::int
));

-- BankDeleteEvidence deletes up to lim observation_evidence rows of the bank (via memory_units).
-- name: BankDeleteEvidence :execrows
DELETE FROM observation_evidence WHERE ctid = ANY(ARRAY(
    SELECT c.ctid FROM observation_evidence c JOIN memory_units p ON p.id = c.observation_id
    WHERE p.tenant_id = @tenant_id AND p.bank_id = @bank_id
    LIMIT @lim::int
));

-- BankDeleteParkedLinks deletes up to lim parked_links rows of the bank.
-- name: BankDeleteParkedLinks :execrows
DELETE FROM parked_links WHERE ctid = ANY(ARRAY(
    SELECT d.ctid FROM parked_links d WHERE d.tenant_id = @tenant_id AND d.bank_id = @bank_id LIMIT @lim::int
));

-- BankDeleteGraphDirty deletes up to lim graph_dirty rows of the bank.
-- name: BankDeleteGraphDirty :execrows
DELETE FROM graph_dirty WHERE ctid = ANY(ARRAY(
    SELECT d.ctid FROM graph_dirty d WHERE d.tenant_id = @tenant_id AND d.bank_id = @bank_id LIMIT @lim::int
));

-- BankDeleteEntityMaintenance deletes up to lim entity_maintenance rows of the bank.
-- name: BankDeleteEntityMaintenance :execrows
DELETE FROM entity_maintenance WHERE ctid = ANY(ARRAY(
    SELECT d.ctid FROM entity_maintenance d WHERE d.tenant_id = @tenant_id AND d.bank_id = @bank_id LIMIT @lim::int
));

-- BankDeleteObservationVersions deletes up to lim observation_versions rows of the bank.
-- name: BankDeleteObservationVersions :execrows
DELETE FROM observation_versions WHERE ctid = ANY(ARRAY(
    SELECT d.ctid FROM observation_versions d WHERE d.tenant_id = @tenant_id AND d.bank_id = @bank_id LIMIT @lim::int
));

-- BankDeleteMemoryAudit deletes up to lim memory_audit rows of the bank.
-- name: BankDeleteMemoryAudit :execrows
DELETE FROM memory_audit WHERE ctid = ANY(ARRAY(
    SELECT d.ctid FROM memory_audit d WHERE d.tenant_id = @tenant_id AND d.bank_id = @bank_id LIMIT @lim::int
));

-- BankDeleteArchive deletes up to lim memory_units_archive rows of the bank.
-- name: BankDeleteArchive :execrows
DELETE FROM memory_units_archive WHERE ctid = ANY(ARRAY(
    SELECT d.ctid FROM memory_units_archive d WHERE d.tenant_id = @tenant_id AND d.bank_id = @bank_id LIMIT @lim::int
));

-- BankDeleteUnits deletes up to lim memory_units rows of the bank.
-- name: BankDeleteUnits :execrows
DELETE FROM memory_units WHERE ctid = ANY(ARRAY(
    SELECT d.ctid FROM memory_units d WHERE d.tenant_id = @tenant_id AND d.bank_id = @bank_id LIMIT @lim::int
));

-- BankDeleteChunks deletes up to lim chunks rows of the bank.
-- name: BankDeleteChunks :execrows
DELETE FROM chunks WHERE ctid = ANY(ARRAY(
    SELECT d.ctid FROM chunks d WHERE d.tenant_id = @tenant_id AND d.bank_id = @bank_id LIMIT @lim::int
));

-- BankDeleteDocuments deletes up to lim documents rows of the bank.
-- name: BankDeleteDocuments :execrows
DELETE FROM documents WHERE ctid = ANY(ARRAY(
    SELECT d.ctid FROM documents d WHERE d.tenant_id = @tenant_id AND d.bank_id = @bank_id LIMIT @lim::int
));

-- BankDeleteEntities deletes up to lim entities rows of the bank.
-- name: BankDeleteEntities :execrows
DELETE FROM entities WHERE ctid = ANY(ARRAY(
    SELECT d.ctid FROM entities d WHERE d.tenant_id = @tenant_id AND d.bank_id = @bank_id LIMIT @lim::int
));

-- BankDeleteObservationScopes deletes up to lim observation_scopes rows of the bank.
-- name: BankDeleteObservationScopes :execrows
DELETE FROM observation_scopes WHERE ctid = ANY(ARRAY(
    SELECT d.ctid FROM observation_scopes d WHERE d.tenant_id = @tenant_id AND d.bank_id = @bank_id LIMIT @lim::int
));

-- BankDeleteModelHistory deletes up to lim mental_model_history rows of the bank.
-- name: BankDeleteModelHistory :execrows
DELETE FROM mental_model_history WHERE ctid = ANY(ARRAY(
    SELECT d.ctid FROM mental_model_history d WHERE d.tenant_id = @tenant_id AND d.bank_id = @bank_id LIMIT @lim::int
));

-- BankDeleteModels deletes up to lim mental_models rows of the bank.
-- name: BankDeleteModels :execrows
DELETE FROM mental_models WHERE ctid = ANY(ARRAY(
    SELECT d.ctid FROM mental_models d WHERE d.tenant_id = @tenant_id AND d.bank_id = @bank_id LIMIT @lim::int
));

-- BankDeleteModelFolders deletes up to lim mental_model_folders rows of the bank.
-- name: BankDeleteModelFolders :execrows
DELETE FROM mental_model_folders WHERE ctid = ANY(ARRAY(
    SELECT d.ctid FROM mental_model_folders d WHERE d.tenant_id = @tenant_id AND d.bank_id = @bank_id LIMIT @lim::int
));

-- BankDeleteDirectives deletes up to lim directives rows of the bank.
-- name: BankDeleteDirectives :execrows
DELETE FROM directives WHERE ctid = ANY(ARRAY(
    SELECT d.ctid FROM directives d WHERE d.tenant_id = @tenant_id AND d.bank_id = @bank_id LIMIT @lim::int
));

-- BankDeleteRow deletes a tombstoned bank's row; a live bank matches nothing.
-- name: BankDeleteRow :execrows
DELETE FROM banks WHERE tenant_id = @tenant_id AND id = @id AND deleting_at IS NOT NULL;

-- BankDeleteEvents deletes every lifecycle event of a bank.
-- name: BankDeleteEvents :exec
DELETE FROM lifecycle_events WHERE tenant_id = @tenant_id AND bank_id = @bank_id;

-- BankDeleteOperations deletes every operation of a bank.
-- name: BankDeleteOperations :exec
DELETE FROM operations WHERE tenant_id = @tenant_id AND bank_id = @bank_id;
