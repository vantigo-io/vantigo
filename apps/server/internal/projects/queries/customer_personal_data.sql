-- name: CustomerProjectsForExport :many
-- CustomerProjectsForExport is a private person's projects for their export
-- (customers GDPR design D2, contracts.CustomerPersonalData): what was done
-- for them, by code, name, status and dates. ix_projects_customer_id finds the
-- rows.
SELECT code, name, status, start_date, end_date
FROM projects.projects
WHERE customer_id = @customer_id::integer
ORDER BY code;
