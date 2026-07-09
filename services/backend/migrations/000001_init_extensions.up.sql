-- Owner: platform (shared primitives used by every module below).

-- gen_random_uuid() and digest helpers used throughout the schema.
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- platform is the shared enumeration of audience-facing networks. Every table
-- that stores per-network data (handles, posts, metrics, quotas, results)
-- references this single type, so adding a network is a one-line change here.
CREATE TYPE platform AS ENUM ('instagram', 'facebook', 'tiktok', 'youtube', 'x', 'linkedin');

-- set_updated_at keeps updated_at honest without trusting callers. Tables with
-- an updated_at column attach a BEFORE UPDATE trigger to this function in their
-- own migration; DROP TABLE removes those triggers, so only the function is
-- owned here.
CREATE OR REPLACE FUNCTION set_updated_at() RETURNS trigger AS $$
BEGIN
    NEW.updated_at := now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
