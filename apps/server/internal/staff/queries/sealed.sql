-- The sealed identifiers (design S4, §4 Sealed fields): the national identity
-- number and the bank account, each written sealed beside what may be kept in
-- clear, and read sealed only by the reveal that opens it and by the
-- personal-data export (personal_data.go), an audited reveal of its own.
-- Nothing else in this module reads a sealed column.

-- name: PersonNationalID :one
-- PersonNationalID is a person's national identity number as stored: its
-- kind, a foreign id's country, the sealed value and its MAC, each NULL when
-- none is on file.
SELECT national_id_kind, national_id_country, national_id_sealed, national_id_mac
FROM staff.people
WHERE id = @id;

-- name: SetPersonNationalID :exec
-- SetPersonNationalID replaces a person's national identity number: the kind
-- and a foreign id's country in clear, the value sealed, and its MAC in the
-- unique column that refuses the same number on two people. The person's
-- revision is left alone: it guards HR's replace of the editable fields,
-- which the sealed ones are not among.
UPDATE staff.people SET
    national_id_kind = @kind,
    national_id_country = sqlc.narg(country),
    national_id_sealed = @sealed,
    national_id_mac = @mac,
    updated_at = @now::timestamptz
WHERE id = @id;

-- name: PersonBankAccount :one
-- PersonBankAccount is a person's bank account as stored: its kind and the
-- sealed value, each NULL when none is on file.
SELECT bank_account_kind, bank_account_sealed
FROM staff.people
WHERE id = @id;

-- name: SetPersonBankAccount :exec
-- SetPersonBankAccount replaces a person's bank account: the kind and the
-- last four digits in clear, the value sealed. The revision is left alone,
-- as SetPersonNationalID leaves it.
UPDATE staff.people SET
    bank_account_kind = @kind,
    bank_account_last4 = @last4,
    bank_account_sealed = @sealed,
    updated_at = @now::timestamptz
WHERE id = @id;
