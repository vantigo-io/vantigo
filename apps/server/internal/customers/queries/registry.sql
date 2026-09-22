-- name: GetCustomerRegistryRecord :one
-- GetCustomerRegistryRecord is GET .../registry-record's own read (Brreg in
-- full design D1, D2): unlocked, since that path never writes. pgx.ErrNoRows
-- means "never fetched", not an error — the endpoint answers 204 for it.
SELECT customer_id, organisation_number, name, organisation_form_code, organisation_form,
       industry_code, industry, employees, vat_registered, bankrupt, under_liquidation,
       under_forced_liquidation, deleted_on, founded_on, website, email, phone, mobile,
       parent_organisation_number, business_address, postal_address, fetched_at, registry_updated_hint
FROM customers.customer_registry_records
WHERE customer_id = @customer_id;

-- name: GetCustomerRegistryRecordForUpdate :one
-- GetCustomerRegistryRecordForUpdate is the read a refresh makes inside the
-- transaction that also writes the row (design D2, D4), after that
-- transaction has already taken LockCustomer on the customer itself.
--
-- The customer lock is what actually serializes two refreshes of the same
-- customer; this FOR UPDATE only holds the record row across the diff and
-- the write that follows it. It could not serialize a first refresh on its
-- own: with no row yet there is nothing to lock, so two concurrent first
-- fetches would each diff against "nothing on file" and each record their
-- own event. pgx.ErrNoRows is exactly that first-fetch case — the handler
-- then compares the registry's name and deletion date against the legal
-- identity's name, and nothing else.
SELECT customer_id, organisation_number, name, organisation_form_code, organisation_form,
       industry_code, industry, employees, vat_registered, bankrupt, under_liquidation,
       under_forced_liquidation, deleted_on, founded_on, website, email, phone, mobile,
       parent_organisation_number, business_address, postal_address, fetched_at, registry_updated_hint
FROM customers.customer_registry_records
WHERE customer_id = @customer_id
FOR UPDATE;

-- name: UpsertCustomerRegistryRecord :exec
-- UpsertCustomerRegistryRecord writes what the registry just said (design
-- D1): one row per customer, so a refresh always overwrites the last record
-- rather than growing a history — the timeline keeps the history, as the
-- registry.change events. Never touches customers.customers, so a fetch can
-- never bump the customer row's own revision.
--
-- registry_updated_hint is deliberately absent from both the insert and the
-- update: delivery B's update-feed worker owns that column, and a fetch made
-- here must neither invent a hint nor erase the one the worker left.
INSERT INTO customers.customer_registry_records (
    customer_id, organisation_number, name, organisation_form_code, organisation_form,
    industry_code, industry, employees, vat_registered, bankrupt, under_liquidation,
    under_forced_liquidation, deleted_on, founded_on, website, email, phone, mobile,
    parent_organisation_number, business_address, postal_address, fetched_at
) VALUES (
    @customer_id, @organisation_number, @name, @organisation_form_code, @organisation_form,
    @industry_code, @industry, @employees, @vat_registered, @bankrupt, @under_liquidation,
    @under_forced_liquidation, @deleted_on, @founded_on, @website, @email, @phone, @mobile,
    @parent_organisation_number, @business_address, @postal_address, @fetched_at::timestamptz
)
ON CONFLICT (customer_id) DO UPDATE SET
    organisation_number        = EXCLUDED.organisation_number,
    name                       = EXCLUDED.name,
    organisation_form_code     = EXCLUDED.organisation_form_code,
    organisation_form          = EXCLUDED.organisation_form,
    industry_code              = EXCLUDED.industry_code,
    industry                   = EXCLUDED.industry,
    employees                  = EXCLUDED.employees,
    vat_registered             = EXCLUDED.vat_registered,
    bankrupt                   = EXCLUDED.bankrupt,
    under_liquidation          = EXCLUDED.under_liquidation,
    under_forced_liquidation   = EXCLUDED.under_forced_liquidation,
    deleted_on                 = EXCLUDED.deleted_on,
    founded_on                 = EXCLUDED.founded_on,
    website                    = EXCLUDED.website,
    email                      = EXCLUDED.email,
    phone                      = EXCLUDED.phone,
    mobile                     = EXCLUDED.mobile,
    parent_organisation_number = EXCLUDED.parent_organisation_number,
    business_address           = EXCLUDED.business_address,
    postal_address             = EXCLUDED.postal_address,
    fetched_at                 = EXCLUDED.fetched_at;

-- name: DeleteCustomerRegistryRecord :exec
-- DeleteCustomerRegistryRecord is what a 410 from the registry means
-- (design D2): the entity is gone from open data entirely, so the copy goes
-- too and only the timeline keeps the fact that it once existed. Deleting a
-- row that is not there is not an error — a customer whose very first
-- refresh answers 410 never had one.
DELETE FROM customers.customer_registry_records
WHERE customer_id = @customer_id;
