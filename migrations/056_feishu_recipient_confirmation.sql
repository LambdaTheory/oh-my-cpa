-- Existing groups require confirmation of the exact target before delivery.
ALTER TABLE feishu_settings ADD COLUMN confirmed_recipient TEXT NOT NULL DEFAULT '';
ALTER TABLE feishu_notifications ADD COLUMN last_error TEXT NOT NULL DEFAULT '';
