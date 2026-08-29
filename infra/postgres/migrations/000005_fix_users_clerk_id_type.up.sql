-- Clerk user IDs are strings such as `user_2NizX9...`, not UUIDs.
-- The live database was created with users.id as UUID, which prevents every
-- authenticated request from resolving its tenant workspace.
ALTER TABLE users
    ALTER COLUMN id TYPE VARCHAR(255)
    USING id::text;
