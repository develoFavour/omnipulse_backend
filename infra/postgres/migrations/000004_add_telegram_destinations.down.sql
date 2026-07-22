ALTER TABLE campaign_deliveries DROP COLUMN IF EXISTS target_type;

ALTER TABLE campaign_deliveries
ALTER COLUMN contact_id SET NOT NULL;

ALTER TABLE campaigns
DROP COLUMN IF EXISTS selected_telegram_destination_ids;

DROP TABLE IF EXISTS telegram_destinations;
