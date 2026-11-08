# Virtual employees, delivery 0: assignment in Communications

Implements design V2 and V3 (`docs/superpowers/specs/2026-11-08-virtual-employees-design.md`, §2, §11, §12).

## Tasks

1. **Contract.** `openapi/communications.yaml` (and the embedded copy, by `go generate`):
   `assignedUserId` on the list becomes a string (`uuid`, `me`, `none`) with a 400;
   `assignedUserName` on the list item and the detail; `GET /assignable-users`; the 422
   `assignee_unknown` on PATCH. Regenerate Go (`go generate`) and `api-schema.d.ts` (`bun run gen:client`).
2. **Queries** (`queries/*.sql`, sqlc): `ListConversations` gets `unassigned_only`;
   `LockConversationAssignee` (FOR UPDATE); `LockConversationForInbound` and
   `FindOpenMessengerConversation` return `assigned_user_id`.
3. **PATCH** (`conversations_patch.go`): `assignedUserId` validated (400 field error, 422
   `assignee_unknown` through `UserDirectory.User`, active only), asked before the transaction;
   inside it the assignee is read FOR UPDATE, compared, and `communications.conversation.assigned`
   published when it changed.
4. **List and detail** (`conversations.go`): `me`/`none`/uuid filter, `assignedUserName`,
   the assignable-users handler.
5. **Events** (`contracts/events_communications.go`): `communications.message.received` v1 from
   the mailbox import (`inbound_import.go`) and the messenger sync (`messenger_import.go`), not for
   a Messenger echo, on the transaction that holds the conversation's lock.
6. **Frontend** (`apps/communications/frontend`): `AssigneeSelect` in the thread (the panel's thread
   is the same component), the assignee avatar on list rows, the Inbox's assignee filter; the host's
   inbox route carries `assignedUserId` in its search. i18n en + nb.
7. **Tests.** Go: `assignment_test.go` (400, 422, the assigned event on the right transitions, the
   filters and names, the assignable users, `message.received` from mail and Messenger, none for an
   echo); the old "unvalidated" PATCH test becomes the 400 test. Frontend: `inbox.test.tsx`.
8. **Documentation** (same series): `user/communications.md` in `en/` and `nb/` (the assignee
   selector, the avatar, the filter, the permission); `reference/communications.md` (the check, the
   list filter, the assignable users, the events). Then `go generate` for the embedded docs.

## Deviations

- The design says the list shows "the assignee's avatar"; the list item carries only the id, so it
  gains `assignedUserName` (resolved in one `UserDirectory.Users` call per page) for the initials.
- The thread's select needs a user search that Communications did not have, so `GET
  /assignable-users` is added (the pattern of Knowledge's and Customers' pickers), under
  `conversations-view`.
- PATCH now writes the assignee it read under the lock when the body does not mention it, so a
  stale pre-transaction read can no longer overwrite a concurrent assignment.
