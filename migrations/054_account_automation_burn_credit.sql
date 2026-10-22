ALTER TABLE account_automation_accounts ADD COLUMN burn_credit INTEGER NOT NULL DEFAULT 0;
ALTER TABLE account_automation_accounts ADD COLUMN removed_at_ms INTEGER;
