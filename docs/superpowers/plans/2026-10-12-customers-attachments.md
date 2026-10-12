# Customers phase 4 delivery E — attachments: implementation plan

**Goal:** a customer and its manual timeline entries carry documents — PDF, JPEG, PNG
or HEIC — stored in the object store under the `customers` scope, moved by a merge,
named by the export and erased by an anonymisation through a deletion ledger.

**Spec:** [`docs/superpowers/specs/2026-10-12-customers-attachments-design.md`](../specs/2026-10-12-customers-attachments-design.md)
(D1–D8).

**Pattern code:** `internal/staff/objects.go` (the accessors, the types, the sniffing,
the file name, the disposition), `internal/staff/documents.go` and
`documents_validation.go` (the upload, the download, the delete, the multipart read),
`internal/staff/object_deletions.go`, `backoff.go` and `queries/retention.sql`'s
ledger statements (the ledger), `internal/expenses/attachments.go` (the
object-before-row order), `internal/customers/addresses.go` (a customer-scoped
sub-resource's handlers and tests), `timeline_events.go` (a generated event),
`merge.go` and `anonymisation.go` (the slots), `apps/customers/frontend/src/pages/
-customer-address-list.tsx` and its test (a card), `-customer-timeline.tsx` (the
entry card and menu), `src/api/import-export.ts` (a multipart request).

## Tasks

- [x] **1. The spec and this plan.**
- [ ] **2. Schema and queries.** Migration `00044_customers_attachments.sql` (the
  table, the ledger); `sqlc.yaml`'s schema list; `queries/attachments.sql` (insert,
  list with the entry join, get, delete, count, lock the entry, the merge move, the
  export list, the erase delete) and `queries/object_deletions.sql` (Staff's five);
  `sqlc generate`.
- [ ] **3. The contract.** `openapi/customers.yaml`: `CustomerAttachment`,
  `CustomerAttachmentTimelineEntry`, the four operations under
  `/customers/{id}/attachments[/{attachmentId}]`, the refusal codes on
  `CustomerConflictProblem`'s description, `CustomerPersonalDataCustomer.attachments`,
  the two permission descriptions; `go generate ./...`; `bun run gen:client`.
- [ ] **4. The backend, tests first.** `attachments_test.go` (the spec's testing
  section, with a fake object store in the harness); `objects.go` (the scoped store,
  the accessors, the types, sniffing, names, keys, the disposition, the bounds, the
  rate-limit policy and the body limit); `attachments.go` (list, upload, download,
  delete, the two events); `object_deletions.go` and `backoff.go` (the ledger, the
  pass, `finishObjectDeletion`); the ledger pass in `anonymisation_worker.go`'s
  cycle; `merge.go` (`MoveCustomerAttachments`, the fifth kind, the noun);
  `personal_data.go` (the export's rows); `anonymisation.go` (the delete and the
  queue, the fifth kind, `fileName` in `personalPayloadKeys`); `module.go` (the
  limits, the body limits, the two descriptions).
- [ ] **5. The frontend, tests first.** `api/attachments.ts` (the query, the upload,
  the delete, the download path); `-customer-attachments-card.tsx` and its test;
  `-customer-timeline.tsx` (the entry's attachments, **Attach file…**, the two event
  types) and its test; `customers.$customerId.tsx` (the card); `i18n.ts` en + nb.
- [ ] **6. Docs, both languages.** `en/reference/customers.md` (the Attachments
  section, the event list, the merge and anonymisation tables, the export table,
  Permissions, API, What comes next); `en/user/customers.md` and
  `nb/user/customers.md` (the card list, an Attachments section, the timeline's
  Attach file…, the merge and personal-data sections, the permissions table);
  `en/admin/object-storage.md` and `nb/admin/object-storage.md` (the scope, the
  paragraph, the download, `sources`); `ROADMAP.md` (phase 4 delivery E, "still
  ahead", phase 6's deferred list).
- [ ] **7. Gates and the pull request.** `mise run server:check`, `server:test`,
  `frontend:check`, `docs:check`; Conventional Commits; the PR on
  `claude/customers-attachments-80c5b3` against `main` with CI green.
