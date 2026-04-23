\restrict dbmate

-- Dumped from database version 16.13
-- Dumped by pg_dump version 18.3

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

SET default_tablespace = '';

SET default_table_access_method = heap;

--
-- Name: credit_ledger; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.credit_ledger (
    id bigint NOT NULL,
    tenant_id uuid NOT NULL,
    user_id uuid,
    delta_millicredits bigint NOT NULL,
    reason text NOT NULL,
    metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: credit_ledger_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.credit_ledger_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: credit_ledger_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.credit_ledger_id_seq OWNED BY public.credit_ledger.id;


--
-- Name: events; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.events (
    id bigint NOT NULL,
    tenant_id uuid NOT NULL,
    user_id uuid,
    event_type text NOT NULL,
    payload jsonb NOT NULL,
    trace_id text,
    created_at timestamp with time zone DEFAULT now() NOT NULL
)
PARTITION BY RANGE (created_at);


--
-- Name: events_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.events_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: events_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.events_id_seq OWNED BY public.events.id;


--
-- Name: events_2026_04; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.events_2026_04 (
    id bigint DEFAULT nextval('public.events_id_seq'::regclass) NOT NULL,
    tenant_id uuid NOT NULL,
    user_id uuid,
    event_type text NOT NULL,
    payload jsonb NOT NULL,
    trace_id text,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: events_2026_05; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.events_2026_05 (
    id bigint DEFAULT nextval('public.events_id_seq'::regclass) NOT NULL,
    tenant_id uuid NOT NULL,
    user_id uuid,
    event_type text NOT NULL,
    payload jsonb NOT NULL,
    trace_id text,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: events_2026_06; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.events_2026_06 (
    id bigint DEFAULT nextval('public.events_id_seq'::regclass) NOT NULL,
    tenant_id uuid NOT NULL,
    user_id uuid,
    event_type text NOT NULL,
    payload jsonb NOT NULL,
    trace_id text,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: events_2026_07; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.events_2026_07 (
    id bigint DEFAULT nextval('public.events_id_seq'::regclass) NOT NULL,
    tenant_id uuid NOT NULL,
    user_id uuid,
    event_type text NOT NULL,
    payload jsonb NOT NULL,
    trace_id text,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: schema_migrations; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.schema_migrations (
    version character varying NOT NULL
);


--
-- Name: tenants; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.tenants (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    name text NOT NULL,
    tier text DEFAULT 'free'::text NOT NULL,
    stripe_customer_id text,
    stripe_subscription_id text,
    credits_balance bigint DEFAULT 100 NOT NULL,
    learning_bucket_b_opt_out boolean DEFAULT false NOT NULL,
    benchmark_contribution_opt_in boolean DEFAULT false NOT NULL,
    byok_config jsonb,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT tenants_tier_check CHECK ((tier = ANY (ARRAY['free'::text, 'hunter'::text, 'operator'::text])))
);


--
-- Name: users; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.users (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    firebase_uid text NOT NULL,
    tenant_id uuid NOT NULL,
    email text NOT NULL,
    display_name text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    last_seen_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: events_2026_04; Type: TABLE ATTACH; Schema: public; Owner: -
--

ALTER TABLE ONLY public.events ATTACH PARTITION public.events_2026_04 FOR VALUES FROM ('2026-04-01 00:00:00+00') TO ('2026-05-01 00:00:00+00');


--
-- Name: events_2026_05; Type: TABLE ATTACH; Schema: public; Owner: -
--

ALTER TABLE ONLY public.events ATTACH PARTITION public.events_2026_05 FOR VALUES FROM ('2026-05-01 00:00:00+00') TO ('2026-06-01 00:00:00+00');


--
-- Name: events_2026_06; Type: TABLE ATTACH; Schema: public; Owner: -
--

ALTER TABLE ONLY public.events ATTACH PARTITION public.events_2026_06 FOR VALUES FROM ('2026-06-01 00:00:00+00') TO ('2026-07-01 00:00:00+00');


--
-- Name: events_2026_07; Type: TABLE ATTACH; Schema: public; Owner: -
--

ALTER TABLE ONLY public.events ATTACH PARTITION public.events_2026_07 FOR VALUES FROM ('2026-07-01 00:00:00+00') TO ('2026-08-01 00:00:00+00');


--
-- Name: credit_ledger id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.credit_ledger ALTER COLUMN id SET DEFAULT nextval('public.credit_ledger_id_seq'::regclass);


--
-- Name: events id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.events ALTER COLUMN id SET DEFAULT nextval('public.events_id_seq'::regclass);


--
-- Name: credit_ledger credit_ledger_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.credit_ledger
    ADD CONSTRAINT credit_ledger_pkey PRIMARY KEY (id);


--
-- Name: events events_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.events
    ADD CONSTRAINT events_pkey PRIMARY KEY (id, created_at);


--
-- Name: events_2026_04 events_2026_04_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.events_2026_04
    ADD CONSTRAINT events_2026_04_pkey PRIMARY KEY (id, created_at);


--
-- Name: events_2026_05 events_2026_05_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.events_2026_05
    ADD CONSTRAINT events_2026_05_pkey PRIMARY KEY (id, created_at);


--
-- Name: events_2026_06 events_2026_06_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.events_2026_06
    ADD CONSTRAINT events_2026_06_pkey PRIMARY KEY (id, created_at);


--
-- Name: events_2026_07 events_2026_07_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.events_2026_07
    ADD CONSTRAINT events_2026_07_pkey PRIMARY KEY (id, created_at);


--
-- Name: schema_migrations schema_migrations_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.schema_migrations
    ADD CONSTRAINT schema_migrations_pkey PRIMARY KEY (version);


--
-- Name: tenants tenants_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tenants
    ADD CONSTRAINT tenants_pkey PRIMARY KEY (id);


--
-- Name: tenants tenants_stripe_customer_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tenants
    ADD CONSTRAINT tenants_stripe_customer_id_key UNIQUE (stripe_customer_id);


--
-- Name: tenants tenants_stripe_subscription_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tenants
    ADD CONSTRAINT tenants_stripe_subscription_id_key UNIQUE (stripe_subscription_id);


--
-- Name: users users_firebase_uid_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_firebase_uid_key UNIQUE (firebase_uid);


--
-- Name: users users_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_pkey PRIMARY KEY (id);


--
-- Name: credit_ledger_tenant_created_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX credit_ledger_tenant_created_idx ON public.credit_ledger USING btree (tenant_id, created_at DESC);


--
-- Name: events_type_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX events_type_idx ON ONLY public.events USING btree (event_type);


--
-- Name: events_2026_04_event_type_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX events_2026_04_event_type_idx ON public.events_2026_04 USING btree (event_type);


--
-- Name: events_tenant_created_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX events_tenant_created_idx ON ONLY public.events USING btree (tenant_id, created_at DESC);


--
-- Name: events_2026_04_tenant_id_created_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX events_2026_04_tenant_id_created_at_idx ON public.events_2026_04 USING btree (tenant_id, created_at DESC);


--
-- Name: events_trace_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX events_trace_idx ON ONLY public.events USING btree (trace_id) WHERE (trace_id IS NOT NULL);


--
-- Name: events_2026_04_trace_id_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX events_2026_04_trace_id_idx ON public.events_2026_04 USING btree (trace_id) WHERE (trace_id IS NOT NULL);


--
-- Name: events_2026_05_event_type_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX events_2026_05_event_type_idx ON public.events_2026_05 USING btree (event_type);


--
-- Name: events_2026_05_tenant_id_created_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX events_2026_05_tenant_id_created_at_idx ON public.events_2026_05 USING btree (tenant_id, created_at DESC);


--
-- Name: events_2026_05_trace_id_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX events_2026_05_trace_id_idx ON public.events_2026_05 USING btree (trace_id) WHERE (trace_id IS NOT NULL);


--
-- Name: events_2026_06_event_type_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX events_2026_06_event_type_idx ON public.events_2026_06 USING btree (event_type);


--
-- Name: events_2026_06_tenant_id_created_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX events_2026_06_tenant_id_created_at_idx ON public.events_2026_06 USING btree (tenant_id, created_at DESC);


--
-- Name: events_2026_06_trace_id_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX events_2026_06_trace_id_idx ON public.events_2026_06 USING btree (trace_id) WHERE (trace_id IS NOT NULL);


--
-- Name: events_2026_07_event_type_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX events_2026_07_event_type_idx ON public.events_2026_07 USING btree (event_type);


--
-- Name: events_2026_07_tenant_id_created_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX events_2026_07_tenant_id_created_at_idx ON public.events_2026_07 USING btree (tenant_id, created_at DESC);


--
-- Name: events_2026_07_trace_id_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX events_2026_07_trace_id_idx ON public.events_2026_07 USING btree (trace_id) WHERE (trace_id IS NOT NULL);


--
-- Name: tenants_stripe_customer_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX tenants_stripe_customer_idx ON public.tenants USING btree (stripe_customer_id);


--
-- Name: tenants_tier_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX tenants_tier_idx ON public.tenants USING btree (tier);


--
-- Name: users_firebase_uid_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX users_firebase_uid_idx ON public.users USING btree (firebase_uid);


--
-- Name: users_tenant_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX users_tenant_idx ON public.users USING btree (tenant_id);


--
-- Name: events_2026_04_event_type_idx; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.events_type_idx ATTACH PARTITION public.events_2026_04_event_type_idx;


--
-- Name: events_2026_04_pkey; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.events_pkey ATTACH PARTITION public.events_2026_04_pkey;


--
-- Name: events_2026_04_tenant_id_created_at_idx; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.events_tenant_created_idx ATTACH PARTITION public.events_2026_04_tenant_id_created_at_idx;


--
-- Name: events_2026_04_trace_id_idx; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.events_trace_idx ATTACH PARTITION public.events_2026_04_trace_id_idx;


--
-- Name: events_2026_05_event_type_idx; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.events_type_idx ATTACH PARTITION public.events_2026_05_event_type_idx;


--
-- Name: events_2026_05_pkey; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.events_pkey ATTACH PARTITION public.events_2026_05_pkey;


--
-- Name: events_2026_05_tenant_id_created_at_idx; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.events_tenant_created_idx ATTACH PARTITION public.events_2026_05_tenant_id_created_at_idx;


--
-- Name: events_2026_05_trace_id_idx; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.events_trace_idx ATTACH PARTITION public.events_2026_05_trace_id_idx;


--
-- Name: events_2026_06_event_type_idx; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.events_type_idx ATTACH PARTITION public.events_2026_06_event_type_idx;


--
-- Name: events_2026_06_pkey; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.events_pkey ATTACH PARTITION public.events_2026_06_pkey;


--
-- Name: events_2026_06_tenant_id_created_at_idx; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.events_tenant_created_idx ATTACH PARTITION public.events_2026_06_tenant_id_created_at_idx;


--
-- Name: events_2026_06_trace_id_idx; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.events_trace_idx ATTACH PARTITION public.events_2026_06_trace_id_idx;


--
-- Name: events_2026_07_event_type_idx; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.events_type_idx ATTACH PARTITION public.events_2026_07_event_type_idx;


--
-- Name: events_2026_07_pkey; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.events_pkey ATTACH PARTITION public.events_2026_07_pkey;


--
-- Name: events_2026_07_tenant_id_created_at_idx; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.events_tenant_created_idx ATTACH PARTITION public.events_2026_07_tenant_id_created_at_idx;


--
-- Name: events_2026_07_trace_id_idx; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.events_trace_idx ATTACH PARTITION public.events_2026_07_trace_id_idx;


--
-- Name: credit_ledger credit_ledger_tenant_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.credit_ledger
    ADD CONSTRAINT credit_ledger_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES public.tenants(id) ON DELETE CASCADE;


--
-- Name: credit_ledger credit_ledger_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.credit_ledger
    ADD CONSTRAINT credit_ledger_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id);


--
-- Name: users users_tenant_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES public.tenants(id) ON DELETE CASCADE;


--
-- PostgreSQL database dump complete
--

\unrestrict dbmate


--
-- Dbmate schema migrations
--

INSERT INTO public.schema_migrations (version) VALUES
    ('20260422000001'),
    ('20260422000002');
