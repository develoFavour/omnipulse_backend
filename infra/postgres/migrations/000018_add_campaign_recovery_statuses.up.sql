DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_type WHERE typname = 'campaign_status') THEN
        ALTER TYPE campaign_status ADD VALUE IF NOT EXISTS 'paused';
        ALTER TYPE campaign_status ADD VALUE IF NOT EXISTS 'cancelled';
    END IF;
END
$$;
