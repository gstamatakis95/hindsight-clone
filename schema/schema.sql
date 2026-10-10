--
-- PostgreSQL database dump
--

SET statement_timeout = 0;
SET lock_timeout = 0;
SET idle_in_transaction_session_timeout = 0;
SET transaction_timeout = 0;
SET client_encoding = 'UTF8';
SET standard_conforming_strings = on;
SELECT pg_catalog.set_config('search_path', '', false);
SET check_function_bodies = false;
SET xmloption = content;
SET client_min_messages = warning;
SET row_security = off;

--
-- Name: paradedb; Type: SCHEMA; Schema: -; Owner: -
--

CREATE SCHEMA paradedb;

--
-- Name: vector; Type: EXTENSION; Schema: -; Owner: -
--

CREATE EXTENSION IF NOT EXISTS vector WITH SCHEMA public;

--
-- Name: pg_search; Type: EXTENSION; Schema: -; Owner: -
--

CREATE EXTENSION IF NOT EXISTS pg_search WITH SCHEMA paradedb;

--
-- Name: pg_trgm; Type: EXTENSION; Schema: -; Owner: -
--

CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public;

SET default_table_access_method = heap;

--
-- Name: banks; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.banks (
    tenant_id text NOT NULL,
    id text NOT NULL,
    name text DEFAULT ''::text NOT NULL,
    config jsonb DEFAULT '{}'::jsonb NOT NULL,
    change_seq bigint DEFAULT 0 NOT NULL,
    deleting_at timestamp with time zone,
    last_memory_write_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

--
-- Name: chunks; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.chunks (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tenant_id text NOT NULL,
    bank_id text NOT NULL,
    document_id text NOT NULL,
    chunk_index integer NOT NULL,
    text text NOT NULL,
    content_hash text DEFAULT ''::text NOT NULL,
    token_count integer DEFAULT 0 NOT NULL,
    truncated boolean DEFAULT false NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

--
-- Name: directives; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.directives (
    tenant_id text NOT NULL,
    bank_id text NOT NULL,
    id text NOT NULL,
    name text DEFAULT ''::text NOT NULL,
    rules text[] DEFAULT '{}'::text[] NOT NULL,
    tags text[] DEFAULT '{}'::text[] NOT NULL,
    active boolean DEFAULT true NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

--
-- Name: documents; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.documents (
    tenant_id text NOT NULL,
    bank_id text NOT NULL,
    id text NOT NULL,
    content_hash text DEFAULT ''::text NOT NULL,
    overrides_hash text DEFAULT ''::text NOT NULL,
    overrides jsonb,
    tags text[] DEFAULT '{}'::text[] NOT NULL,
    blob_key text DEFAULT ''::text NOT NULL,
    extracted_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

--
-- Name: entities; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.entities (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tenant_id text NOT NULL,
    bank_id text NOT NULL,
    canonical_name text NOT NULL,
    kind text DEFAULT ''::text NOT NULL,
    label_value text,
    mention_count integer DEFAULT 0 NOT NULL,
    first_seen timestamp with time zone,
    last_seen timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

--
-- Name: entity_cooccurrences; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.entity_cooccurrences (
    tenant_id text NOT NULL,
    bank_id text NOT NULL,
    entity_a uuid NOT NULL,
    entity_b uuid NOT NULL,
    count integer DEFAULT 0 NOT NULL,
    last_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT entity_cooccurrences_ordered CHECK ((entity_a < entity_b))
);

--
-- Name: entity_maintenance; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.entity_maintenance (
    tenant_id text NOT NULL,
    bank_id text NOT NULL,
    entity_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

--
-- Name: graph_dirty; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.graph_dirty (
    tenant_id text NOT NULL,
    bank_id text NOT NULL,
    unit_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

--
-- Name: lifecycle_events; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.lifecycle_events (
    id bigint NOT NULL,
    tenant_id text NOT NULL,
    bank_id text NOT NULL,
    seq bigint NOT NULL,
    type text NOT NULL,
    at timestamp with time zone DEFAULT now() NOT NULL,
    operation_id uuid,
    subject_id text DEFAULT ''::text NOT NULL
);

--
-- Name: lifecycle_events_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.lifecycle_events_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

--
-- Name: lifecycle_events_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.lifecycle_events_id_seq OWNED BY public.lifecycle_events.id;

--
-- Name: memory_audit; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.memory_audit (
    id bigint NOT NULL,
    tenant_id text NOT NULL,
    bank_id text NOT NULL,
    unit_id uuid NOT NULL,
    action text NOT NULL,
    reason text DEFAULT ''::text NOT NULL,
    before jsonb,
    after jsonb,
    operation_id uuid,
    at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT memory_audit_action_check CHECK ((action = ANY (ARRAY['edit'::text, 'invalidate'::text, 'restore'::text, 'reprocess'::text])))
);

--
-- Name: memory_audit_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.memory_audit_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

--
-- Name: memory_audit_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.memory_audit_id_seq OWNED BY public.memory_audit.id;

--
-- Name: memory_links; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.memory_links (
    tenant_id text NOT NULL,
    bank_id text NOT NULL,
    from_unit_id uuid NOT NULL,
    to_unit_id uuid NOT NULL,
    kind text NOT NULL,
    weight double precision DEFAULT 1 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT memory_links_kind_check CHECK ((kind = ANY (ARRAY['temporal'::text, 'semantic'::text, 'causal'::text]))),
    CONSTRAINT memory_links_no_self_link CHECK ((from_unit_id <> to_unit_id))
);

--
-- Name: memory_units; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.memory_units (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tenant_id text NOT NULL,
    bank_id text NOT NULL,
    document_id text,
    chunk_id uuid,
    fact_type text NOT NULL,
    text text NOT NULL,
    context text DEFAULT ''::text NOT NULL,
    metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    tags text[] DEFAULT '{}'::text[] NOT NULL,
    embedding public.vector(768),
    occurred_start timestamp with time zone,
    occurred_end timestamp with time zone,
    mentioned_at timestamp with time zone,
    timeless boolean DEFAULT false NOT NULL,
    proof_count integer DEFAULT 0 NOT NULL,
    scope_key text,
    version bigint DEFAULT 1 NOT NULL,
    seq bigint NOT NULL,
    consolidated_at timestamp with time zone,
    consolidation_failed_at timestamp with time zone,
    edited_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT memory_units_fact_type_check CHECK ((fact_type = ANY (ARRAY['world'::text, 'experience'::text, 'observation'::text]))),
    CONSTRAINT memory_units_scope_key_observation_only CHECK (((scope_key IS NULL) OR (fact_type = 'observation'::text)))
);

--
-- Name: memory_units_archive; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.memory_units_archive (
    id uuid NOT NULL,
    tenant_id text NOT NULL,
    bank_id text NOT NULL,
    document_id text,
    chunk_id uuid,
    fact_type text NOT NULL,
    text text NOT NULL,
    context text DEFAULT ''::text NOT NULL,
    metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    tags text[] DEFAULT '{}'::text[] NOT NULL,
    occurred_start timestamp with time zone,
    occurred_end timestamp with time zone,
    mentioned_at timestamp with time zone,
    timeless boolean DEFAULT false NOT NULL,
    seq bigint NOT NULL,
    edited_at timestamp with time zone,
    entities_snapshot jsonb DEFAULT '[]'::jsonb NOT NULL,
    links_snapshot jsonb DEFAULT '[]'::jsonb NOT NULL,
    reason text DEFAULT ''::text NOT NULL,
    operation_id uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    archived_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT memory_units_archive_fact_type_check CHECK ((fact_type = ANY (ARRAY['world'::text, 'experience'::text, 'observation'::text])))
);

--
-- Name: mental_model_folders; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.mental_model_folders (
    tenant_id text NOT NULL,
    bank_id text NOT NULL,
    path text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

--
-- Name: mental_model_history; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.mental_model_history (
    id bigint NOT NULL,
    tenant_id text NOT NULL,
    bank_id text NOT NULL,
    mental_model_id text NOT NULL,
    kind text NOT NULL,
    version bigint,
    previous_content text DEFAULT ''::text NOT NULL,
    outcome text DEFAULT ''::text NOT NULL,
    failure_reason text DEFAULT ''::text NOT NULL,
    error_message text DEFAULT ''::text NOT NULL,
    changed_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT mental_model_history_kind_check CHECK ((kind = ANY (ARRAY['version'::text, 'refresh_failed'::text])))
);

--
-- Name: mental_model_history_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.mental_model_history_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

--
-- Name: mental_model_history_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.mental_model_history_id_seq OWNED BY public.mental_model_history.id;

--
-- Name: mental_models; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.mental_models (
    tenant_id text NOT NULL,
    bank_id text NOT NULL,
    id text NOT NULL,
    name text DEFAULT ''::text NOT NULL,
    source_query text DEFAULT ''::text NOT NULL,
    folder_path text DEFAULT ''::text NOT NULL,
    tags text[] DEFAULT '{}'::text[] NOT NULL,
    max_tokens integer,
    trigger jsonb DEFAULT '{}'::jsonb NOT NULL,
    overrides jsonb,
    content_md text DEFAULT ''::text NOT NULL,
    structured_output_json jsonb,
    version bigint DEFAULT 0 NOT NULL,
    is_stale boolean DEFAULT false NOT NULL,
    last_refreshed_at timestamp with time zone,
    last_seq_seen bigint DEFAULT 0 NOT NULL,
    last_refresh_failed_at timestamp with time zone,
    auto_refresh_paused boolean DEFAULT false NOT NULL,
    source_ids uuid[] DEFAULT '{}'::uuid[] NOT NULL,
    last_trace jsonb,
    embedding public.vector(768),
    refresh_cron text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

--
-- Name: observation_evidence; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.observation_evidence (
    tenant_id text NOT NULL,
    bank_id text NOT NULL,
    observation_id uuid NOT NULL,
    unit_id uuid NOT NULL,
    quote text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

--
-- Name: observation_scopes; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.observation_scopes (
    tenant_id text NOT NULL,
    bank_id text NOT NULL,
    scope_key text NOT NULL,
    tags text[] DEFAULT '{}'::text[] NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

--
-- Name: observation_versions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.observation_versions (
    id bigint NOT NULL,
    tenant_id text NOT NULL,
    bank_id text NOT NULL,
    observation_id uuid NOT NULL,
    version bigint NOT NULL,
    text text NOT NULL,
    source_ids uuid[] DEFAULT '{}'::uuid[] NOT NULL,
    at timestamp with time zone DEFAULT now() NOT NULL
);

--
-- Name: observation_versions_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.observation_versions_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

--
-- Name: observation_versions_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.observation_versions_id_seq OWNED BY public.observation_versions.id;

--
-- Name: operations; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.operations (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tenant_id text NOT NULL,
    bank_id text NOT NULL,
    kind text NOT NULL,
    status text DEFAULT 'pending'::text NOT NULL,
    serial_key text,
    payload jsonb DEFAULT '{}'::jsonb NOT NULL,
    overrides jsonb,
    parent_id uuid,
    attempts integer DEFAULT 0 NOT NULL,
    run_at timestamp with time zone DEFAULT now() NOT NULL,
    worker_id text,
    heartbeat_at timestamp with time zone,
    progress jsonb,
    usage jsonb DEFAULT '[]'::jsonb NOT NULL,
    result jsonb,
    error text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    finished_at timestamp with time zone,
    CONSTRAINT operations_kind_check CHECK ((kind = ANY (ARRAY['retain_batch'::text, 'retain_document'::text, 'consolidate'::text, 'rederive_memory'::text, 'retag_document'::text, 'refresh_mental_model'::text, 'graph_maintenance'::text, 'delete_bank'::text]))),
    CONSTRAINT operations_status_check CHECK ((status = ANY (ARRAY['pending'::text, 'processing'::text, 'completed'::text, 'failed'::text, 'cancelled'::text])))
);

--
-- Name: parked_links; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.parked_links (
    tenant_id text NOT NULL,
    bank_id text NOT NULL,
    archived_unit_id uuid NOT NULL,
    other_unit_id uuid NOT NULL,
    kind text NOT NULL,
    direction text NOT NULL,
    weight double precision DEFAULT 1 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT parked_links_direction_check CHECK ((direction = ANY (ARRAY['out'::text, 'in'::text])))
);

--
-- Name: tenants; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.tenants (
    id text NOT NULL,
    config jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

--
-- Name: unit_entities; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.unit_entities (
    tenant_id text NOT NULL,
    bank_id text NOT NULL,
    unit_id uuid NOT NULL,
    entity_id uuid NOT NULL
);

--
-- Name: lifecycle_events id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.lifecycle_events ALTER COLUMN id SET DEFAULT nextval('public.lifecycle_events_id_seq'::regclass);

--
-- Name: memory_audit id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.memory_audit ALTER COLUMN id SET DEFAULT nextval('public.memory_audit_id_seq'::regclass);

--
-- Name: mental_model_history id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.mental_model_history ALTER COLUMN id SET DEFAULT nextval('public.mental_model_history_id_seq'::regclass);

--
-- Name: observation_versions id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.observation_versions ALTER COLUMN id SET DEFAULT nextval('public.observation_versions_id_seq'::regclass);

--
-- Name: banks banks_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.banks
    ADD CONSTRAINT banks_pkey PRIMARY KEY (tenant_id, id);

--
-- Name: chunks chunks_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chunks
    ADD CONSTRAINT chunks_pkey PRIMARY KEY (id);

--
-- Name: chunks chunks_tenant_id_bank_id_document_id_chunk_index_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chunks
    ADD CONSTRAINT chunks_tenant_id_bank_id_document_id_chunk_index_key UNIQUE (tenant_id, bank_id, document_id, chunk_index);

--
-- Name: directives directives_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.directives
    ADD CONSTRAINT directives_pkey PRIMARY KEY (tenant_id, bank_id, id);

--
-- Name: documents documents_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.documents
    ADD CONSTRAINT documents_pkey PRIMARY KEY (tenant_id, bank_id, id);

--
-- Name: entities entities_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.entities
    ADD CONSTRAINT entities_pkey PRIMARY KEY (id);

--
-- Name: entity_cooccurrences entity_cooccurrences_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.entity_cooccurrences
    ADD CONSTRAINT entity_cooccurrences_pkey PRIMARY KEY (entity_a, entity_b);

--
-- Name: entity_maintenance entity_maintenance_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.entity_maintenance
    ADD CONSTRAINT entity_maintenance_pkey PRIMARY KEY (tenant_id, bank_id, entity_id);

--
-- Name: graph_dirty graph_dirty_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.graph_dirty
    ADD CONSTRAINT graph_dirty_pkey PRIMARY KEY (tenant_id, bank_id, unit_id);

--
-- Name: lifecycle_events lifecycle_events_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.lifecycle_events
    ADD CONSTRAINT lifecycle_events_pkey PRIMARY KEY (id);

--
-- Name: memory_audit memory_audit_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.memory_audit
    ADD CONSTRAINT memory_audit_pkey PRIMARY KEY (id);

--
-- Name: memory_links memory_links_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.memory_links
    ADD CONSTRAINT memory_links_pkey PRIMARY KEY (from_unit_id, to_unit_id, kind);

--
-- Name: memory_units_archive memory_units_archive_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.memory_units_archive
    ADD CONSTRAINT memory_units_archive_pkey PRIMARY KEY (id);

--
-- Name: memory_units memory_units_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.memory_units
    ADD CONSTRAINT memory_units_pkey PRIMARY KEY (id);

--
-- Name: mental_model_folders mental_model_folders_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.mental_model_folders
    ADD CONSTRAINT mental_model_folders_pkey PRIMARY KEY (tenant_id, bank_id, path);

--
-- Name: mental_model_history mental_model_history_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.mental_model_history
    ADD CONSTRAINT mental_model_history_pkey PRIMARY KEY (id);

--
-- Name: mental_models mental_models_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.mental_models
    ADD CONSTRAINT mental_models_pkey PRIMARY KEY (tenant_id, bank_id, id);

--
-- Name: observation_evidence observation_evidence_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.observation_evidence
    ADD CONSTRAINT observation_evidence_pkey PRIMARY KEY (observation_id, unit_id);

--
-- Name: observation_scopes observation_scopes_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.observation_scopes
    ADD CONSTRAINT observation_scopes_pkey PRIMARY KEY (tenant_id, bank_id, scope_key);

--
-- Name: observation_versions observation_versions_observation_id_version_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.observation_versions
    ADD CONSTRAINT observation_versions_observation_id_version_key UNIQUE (observation_id, version);

--
-- Name: observation_versions observation_versions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.observation_versions
    ADD CONSTRAINT observation_versions_pkey PRIMARY KEY (id);

--
-- Name: operations operations_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.operations
    ADD CONSTRAINT operations_pkey PRIMARY KEY (id);

--
-- Name: parked_links parked_links_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.parked_links
    ADD CONSTRAINT parked_links_pkey PRIMARY KEY (archived_unit_id, other_unit_id, kind, direction);

--
-- Name: tenants tenants_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tenants
    ADD CONSTRAINT tenants_pkey PRIMARY KEY (id);

--
-- Name: unit_entities unit_entities_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.unit_entities
    ADD CONSTRAINT unit_entities_pkey PRIMARY KEY (unit_id, entity_id);

--
-- Name: documents_tags_gin; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX documents_tags_gin ON public.documents USING gin (tags);

--
-- Name: entities_label_uniq; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX entities_label_uniq ON public.entities USING btree (tenant_id, bank_id, kind, label_value) WHERE (label_value IS NOT NULL);

--
-- Name: entities_name_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX entities_name_idx ON public.entities USING btree (tenant_id, bank_id, lower(canonical_name));

--
-- Name: entities_name_trgm; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX entities_name_trgm ON public.entities USING gin (lower(canonical_name) public.gin_trgm_ops);

--
-- Name: entity_cooccurrences_entity_b_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX entity_cooccurrences_entity_b_idx ON public.entity_cooccurrences USING btree (entity_b);

--
-- Name: lifecycle_events_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX lifecycle_events_at_idx ON public.lifecycle_events USING btree (at);

--
-- Name: lifecycle_events_seq_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX lifecycle_events_seq_idx ON public.lifecycle_events USING btree (tenant_id, bank_id, seq);

--
-- Name: memory_audit_operation_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX memory_audit_operation_idx ON public.memory_audit USING btree (operation_id) WHERE (operation_id IS NOT NULL);

--
-- Name: memory_audit_unit_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX memory_audit_unit_idx ON public.memory_audit USING btree (tenant_id, bank_id, unit_id, at);

--
-- Name: memory_links_to_unit_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX memory_links_to_unit_idx ON public.memory_links USING btree (to_unit_id);

--
-- Name: memory_units_archive_bank_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX memory_units_archive_bank_idx ON public.memory_units_archive USING btree (tenant_id, bank_id, archived_at);

--
-- Name: memory_units_archive_document_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX memory_units_archive_document_idx ON public.memory_units_archive USING btree (tenant_id, bank_id, document_id);

--
-- Name: memory_units_archive_operation_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX memory_units_archive_operation_idx ON public.memory_units_archive USING btree (operation_id) WHERE (operation_id IS NOT NULL);

--
-- Name: memory_units_bm25; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX memory_units_bm25 ON public.memory_units USING bm25 (id, text, context, ((tenant_id)::pdb.literal), ((bank_id)::pdb.literal), ((fact_type)::pdb.literal));

--
-- Name: memory_units_chunk_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX memory_units_chunk_idx ON public.memory_units USING btree (chunk_id) WHERE (chunk_id IS NOT NULL);

--
-- Name: memory_units_created_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX memory_units_created_at_idx ON public.memory_units USING btree (tenant_id, bank_id, created_at);

--
-- Name: memory_units_document_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX memory_units_document_idx ON public.memory_units USING btree (tenant_id, bank_id, document_id);

--
-- Name: memory_units_embedding_hnsw; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX memory_units_embedding_hnsw ON public.memory_units USING hnsw (embedding public.vector_cosine_ops);

--
-- Name: memory_units_observation_scope_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX memory_units_observation_scope_idx ON public.memory_units USING btree (tenant_id, bank_id, scope_key) WHERE (fact_type = 'observation'::text);

--
-- Name: memory_units_occurred_start_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX memory_units_occurred_start_idx ON public.memory_units USING btree (tenant_id, bank_id, occurred_start);

--
-- Name: memory_units_seq_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX memory_units_seq_idx ON public.memory_units USING btree (tenant_id, bank_id, seq);

--
-- Name: memory_units_tags_gin; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX memory_units_tags_gin ON public.memory_units USING gin (tags);

--
-- Name: memory_units_unconsolidated_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX memory_units_unconsolidated_idx ON public.memory_units USING btree (tenant_id, bank_id, created_at) WHERE ((consolidated_at IS NULL) AND (consolidation_failed_at IS NULL) AND (fact_type <> 'observation'::text));

--
-- Name: mental_model_history_model_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX mental_model_history_model_idx ON public.mental_model_history USING btree (tenant_id, bank_id, mental_model_id, kind, changed_at);

--
-- Name: mental_models_embedding_hnsw; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX mental_models_embedding_hnsw ON public.mental_models USING hnsw (embedding public.vector_cosine_ops);

--
-- Name: mental_models_refresh_cron_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX mental_models_refresh_cron_idx ON public.mental_models USING btree (refresh_cron) WHERE (refresh_cron IS NOT NULL);

--
-- Name: mental_models_tags_gin; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX mental_models_tags_gin ON public.mental_models USING gin (tags);

--
-- Name: observation_evidence_unit_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX observation_evidence_unit_idx ON public.observation_evidence USING btree (unit_id);

--
-- Name: observation_versions_bank_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX observation_versions_bank_idx ON public.observation_versions USING btree (tenant_id, bank_id);

--
-- Name: operations_active_dedupe_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX operations_active_dedupe_idx ON public.operations USING btree (tenant_id, bank_id, kind, serial_key) WHERE (status = ANY (ARRAY['pending'::text, 'processing'::text]));

--
-- Name: operations_bank_created_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX operations_bank_created_idx ON public.operations USING btree (tenant_id, bank_id, created_at);

--
-- Name: operations_parent_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX operations_parent_idx ON public.operations USING btree (parent_id) WHERE (parent_id IS NOT NULL);

--
-- Name: operations_pending_run_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX operations_pending_run_at_idx ON public.operations USING btree (run_at) WHERE (status = 'pending'::text);

--
-- Name: operations_processing_heartbeat_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX operations_processing_heartbeat_idx ON public.operations USING btree (heartbeat_at) WHERE (status = 'processing'::text);

--
-- Name: operations_processing_serial_key_uniq; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX operations_processing_serial_key_uniq ON public.operations USING btree (tenant_id, bank_id, serial_key) WHERE (status = 'processing'::text);

--
-- Name: operations_terminal_finished_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX operations_terminal_finished_idx ON public.operations USING btree (finished_at) WHERE (status = ANY (ARRAY['completed'::text, 'failed'::text, 'cancelled'::text]));

--
-- Name: parked_links_other_unit_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX parked_links_other_unit_idx ON public.parked_links USING btree (other_unit_id);

--
-- Name: unit_entities_entity_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX unit_entities_entity_idx ON public.unit_entities USING btree (entity_id);

--
-- Name: chunks chunks_tenant_id_bank_id_document_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chunks
    ADD CONSTRAINT chunks_tenant_id_bank_id_document_id_fkey FOREIGN KEY (tenant_id, bank_id, document_id) REFERENCES public.documents(tenant_id, bank_id, id) ON DELETE CASCADE;

--
-- Name: directives directives_tenant_id_bank_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.directives
    ADD CONSTRAINT directives_tenant_id_bank_id_fkey FOREIGN KEY (tenant_id, bank_id) REFERENCES public.banks(tenant_id, id) ON DELETE CASCADE;

--
-- Name: documents documents_tenant_id_bank_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.documents
    ADD CONSTRAINT documents_tenant_id_bank_id_fkey FOREIGN KEY (tenant_id, bank_id) REFERENCES public.banks(tenant_id, id) ON DELETE CASCADE;

--
-- Name: entities entities_tenant_id_bank_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.entities
    ADD CONSTRAINT entities_tenant_id_bank_id_fkey FOREIGN KEY (tenant_id, bank_id) REFERENCES public.banks(tenant_id, id) ON DELETE CASCADE;

--
-- Name: entity_cooccurrences entity_cooccurrences_entity_a_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.entity_cooccurrences
    ADD CONSTRAINT entity_cooccurrences_entity_a_fkey FOREIGN KEY (entity_a) REFERENCES public.entities(id) ON DELETE CASCADE;

--
-- Name: entity_cooccurrences entity_cooccurrences_entity_b_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.entity_cooccurrences
    ADD CONSTRAINT entity_cooccurrences_entity_b_fkey FOREIGN KEY (entity_b) REFERENCES public.entities(id) ON DELETE CASCADE;

--
-- Name: memory_links memory_links_from_unit_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.memory_links
    ADD CONSTRAINT memory_links_from_unit_id_fkey FOREIGN KEY (from_unit_id) REFERENCES public.memory_units(id) ON DELETE CASCADE;

--
-- Name: memory_links memory_links_to_unit_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.memory_links
    ADD CONSTRAINT memory_links_to_unit_id_fkey FOREIGN KEY (to_unit_id) REFERENCES public.memory_units(id) ON DELETE CASCADE;

--
-- Name: memory_units memory_units_chunk_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.memory_units
    ADD CONSTRAINT memory_units_chunk_id_fkey FOREIGN KEY (chunk_id) REFERENCES public.chunks(id) ON DELETE SET NULL;

--
-- Name: memory_units memory_units_tenant_id_bank_id_document_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.memory_units
    ADD CONSTRAINT memory_units_tenant_id_bank_id_document_id_fkey FOREIGN KEY (tenant_id, bank_id, document_id) REFERENCES public.documents(tenant_id, bank_id, id) ON DELETE CASCADE;

--
-- Name: memory_units memory_units_tenant_id_bank_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.memory_units
    ADD CONSTRAINT memory_units_tenant_id_bank_id_fkey FOREIGN KEY (tenant_id, bank_id) REFERENCES public.banks(tenant_id, id) ON DELETE CASCADE;

--
-- Name: mental_model_folders mental_model_folders_tenant_id_bank_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.mental_model_folders
    ADD CONSTRAINT mental_model_folders_tenant_id_bank_id_fkey FOREIGN KEY (tenant_id, bank_id) REFERENCES public.banks(tenant_id, id) ON DELETE CASCADE;

--
-- Name: mental_model_history mental_model_history_tenant_id_bank_id_mental_model_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.mental_model_history
    ADD CONSTRAINT mental_model_history_tenant_id_bank_id_mental_model_id_fkey FOREIGN KEY (tenant_id, bank_id, mental_model_id) REFERENCES public.mental_models(tenant_id, bank_id, id) ON DELETE CASCADE;

--
-- Name: mental_models mental_models_tenant_id_bank_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.mental_models
    ADD CONSTRAINT mental_models_tenant_id_bank_id_fkey FOREIGN KEY (tenant_id, bank_id) REFERENCES public.banks(tenant_id, id) ON DELETE CASCADE;

--
-- Name: observation_evidence observation_evidence_observation_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.observation_evidence
    ADD CONSTRAINT observation_evidence_observation_id_fkey FOREIGN KEY (observation_id) REFERENCES public.memory_units(id) ON DELETE CASCADE;

--
-- Name: observation_evidence observation_evidence_unit_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.observation_evidence
    ADD CONSTRAINT observation_evidence_unit_id_fkey FOREIGN KEY (unit_id) REFERENCES public.memory_units(id) ON DELETE CASCADE;

--
-- Name: observation_scopes observation_scopes_tenant_id_bank_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.observation_scopes
    ADD CONSTRAINT observation_scopes_tenant_id_bank_id_fkey FOREIGN KEY (tenant_id, bank_id) REFERENCES public.banks(tenant_id, id) ON DELETE CASCADE;

--
-- Name: unit_entities unit_entities_entity_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.unit_entities
    ADD CONSTRAINT unit_entities_entity_id_fkey FOREIGN KEY (entity_id) REFERENCES public.entities(id) ON DELETE CASCADE;

--
-- Name: unit_entities unit_entities_unit_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.unit_entities
    ADD CONSTRAINT unit_entities_unit_id_fkey FOREIGN KEY (unit_id) REFERENCES public.memory_units(id) ON DELETE CASCADE;

--
-- PostgreSQL database dump complete
--

