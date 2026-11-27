-- Configurable show policy column for HP1 (Tier 1: pre-call song replacement).
-- Aurora DSQL requires ADD COLUMN without inline NOT NULL or DEFAULT constraints;
-- readers apply COALESCE(allow_song_replacement, FALSE) so historical rows stay false
-- while new shows default to TRUE in application creation logic.

ALTER TABLE events ADD COLUMN IF NOT EXISTS allow_song_replacement BOOLEAN;
