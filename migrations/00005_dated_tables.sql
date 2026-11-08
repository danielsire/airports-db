-- +goose Up
-- The existing (empty) tables become schema templates for the dated tables.
ALTER TABLE airport RENAME TO airport_template;
ALTER TABLE runway  RENAME TO runway_template;

-- The names the repositories query. Until the first load they show the empty templates.
CREATE VIEW airport AS SELECT * FROM airport_template;
CREATE VIEW runway  AS SELECT * FROM runway_template;

-- +goose Down
DROP VIEW runway;
DROP VIEW airport;
ALTER TABLE runway_template  RENAME TO runway;
ALTER TABLE airport_template RENAME TO airport;
