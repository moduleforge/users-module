-- Helper functions used across migrations.

-- set_updated_at() — generic trigger function that stamps updated_at = now()
-- on any table that has an updated_at column. Applied via BEFORE UPDATE triggers.
CREATE OR REPLACE FUNCTION set_updated_at() RETURNS TRIGGER AS $$
BEGIN
  NEW.updated_at = now();
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;
