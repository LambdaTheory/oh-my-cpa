-- Ollama's documented balance endpoint authenticates with a Bearer API key, while
-- the settings-page scraper authenticates with a session cookie. An account may now
-- carry both: the key is the preferred path and the cookie stays as the fallback.
--
-- The key is a high-value secret with the same handling as the cookie: AES-GCM
-- encrypted beside its nonce, never selected by an ordinary metadata read, never
-- returned by the API, and never written to a log, an error or an audit row. The
-- columns are nullable and carry no default and no backfill, so an existing account
-- keeps working on the cookie path and "never had a key" stays distinguishable from
-- an empty value.
ALTER TABLE ollama_accounts ADD COLUMN api_key_ciphertext BLOB;
ALTER TABLE ollama_accounts ADD COLUMN api_key_nonce BLOB;
ALTER TABLE ollama_accounts ADD COLUMN api_key_updated_at_ms INTEGER;
