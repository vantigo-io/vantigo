-- +goose Up
-- Staff's provider-neutral names (Google Workspace design G6,
-- docs/superpowers/specs/2026-11-03-google-workspace-design.md): the code
-- Staff itself records on a provider link when the person's manager has no
-- account in the directory is manager_not_in_directory, no longer
-- manager_not_in_entra. The links the accounts worker wrote before carry the
-- old code in reported_codes and, when it was the step's error, in
-- last_error; both are rewritten so the attention item and the Account tab
-- read them. Staff's schema alone; the provider never wrote the code (it
-- reports manager_not_found).
UPDATE staff.identity_links
SET reported_codes = array_replace(reported_codes, 'manager_not_in_entra', 'manager_not_in_directory')
WHERE 'manager_not_in_entra' = ANY (reported_codes);

UPDATE staff.identity_links
SET last_error = replace(last_error, 'manager_not_in_entra', 'manager_not_in_directory')
WHERE last_error LIKE '%manager\_not\_in\_entra%';

-- +goose Down
UPDATE staff.identity_links
SET reported_codes = array_replace(reported_codes, 'manager_not_in_directory', 'manager_not_in_entra')
WHERE 'manager_not_in_directory' = ANY (reported_codes);

UPDATE staff.identity_links
SET last_error = replace(last_error, 'manager_not_in_directory', 'manager_not_in_entra')
WHERE last_error LIKE '%manager\_not\_in\_directory%';
