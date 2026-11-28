DROP INDEX IF EXISTS issues_cycle;
DROP INDEX IF EXISTS issues_project;
ALTER TABLE issues DROP COLUMN cycle_id;
ALTER TABLE issues DROP COLUMN project_id;
DROP TABLE IF EXISTS issue_cycles;
DROP TABLE IF EXISTS issue_projects;
