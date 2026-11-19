---
title: "API compatibility"
description: "What Vantigo promises an integration about /api/v1: additive changes only, breaking changes as a new operation or version, deprecation with Deprecation and Sunset headers at least six months ahead, what the promise does not cover, and how a pull request that breaks it is caught."
sidebar:
  order: 2
sources:
  - tools/api
  - .github/release.yml
---

This is the promise Vantigo makes to an application that calls its API with an API key (see the
[introduction](/en/reference/api-introduction/)). It covers every operation in the document
`GET /api/openapi.json` answers a key — the operations a key can call.

## Within /api/v1, changes are additive

A release may, without notice beyond its release notes:

- add an operation;
- add an optional field to a request, an optional query parameter, or an optional request header
  (such as `Idempotency-Key` on the operations that create, see
  [Retrying safely](/en/reference/api-introduction/#retrying-safely)), with the answers it brings;
- add a field to a response;
- add a value to an enum in a response.

A client must therefore ignore response fields it does not know, and treat an enum value it does
not know as "other" rather than failing.

## A breaking change ships beside, never in place

Removing or renaming an operation, a field or an enum value, making an optional request field
required, narrowing what a field accepts, or changing what an answer means is a breaking change.
Such a change to an operation a key can call ships as a **new operation**, or under a **new
version** of the path, while the old one keeps working.

## Deprecation

An operation that is going away is announced in the release notes and answers with:

- `Deprecation`, the date it was deprecated;
- `Sunset`, the date after which it may be removed.

The sunset is at least **six months** after the release that announces it. A deprecated operation
keeps working, unchanged, until then.

## What the promise does not cover

- Operations no key can call: those behind a person's session, an Owner's policy, SSO, SCIM, and
  those marked as never available to API keys. They are the application's own screens' API and
  change with them.
- The wording of `error.message`, the order of fields in a response, and the order of items a
  list answers without an explicit sort.
- Undocumented behaviour, and anything the document does not describe.
- A module an installation has switched off: its operations are not served, and the document
  leaves them out.

## How a breaking change is caught

Every pull request to Vantigo builds the integrator's document — every operation a key can call,
with every module enabled (`vantigo openapi integrator`) — for itself and for the commit it is
based on, and compares the two with [oasdiff](https://github.com/oasdiff/oasdiff). What this page counts as
compatible is not a finding: a value added to a response enum is lowered to information
([`tools/api/oasdiff-severity.txt`](https://github.com/vantigo-io/vantigo/blob/main/tools/api/oasdiff-severity.txt)). A breaking
finding fails the pull request unless one of its commits says so on purpose: a Conventional
Commits subject with `!` (`feat(orders)!: …`) and, for each operation, a trailer

```text
API-Breaking: getOrders — the total moves to totals.gross; getOrdersV2 replaces it
```

The pull request is then labelled `breaking-api`, and the release notes list it under
**Breaking API changes**. How contributors use it is in
[Documentation](/en/contributing/documentation/#the-api-contract-and-integrators).
