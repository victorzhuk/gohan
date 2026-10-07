ALTER TABLE checkpoint_record ALTER COLUMN input TYPE bytea USING input::text::bytea;
