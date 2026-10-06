---
title: Payments from the bank
description: The bank agreement that gives you OCR giro or camt.054 files of incoming payments, where to download them, and the format each account's files are imported in.
sidebar:
  order: 43
sources:
  - apps/server/internal/invoices/bankfile
  - apps/server/internal/invoices/bankimport.go
  - apps/server/internal/invoices/bankaccounts.go
---

Vantigo reads the bank's own record of the money that arrived on the seller's account: a
file of incoming payments, downloaded from the online bank and imported in Invoices by
someone with `invoices:payments`. Each payment in it carries the KID printed on the
invoice, which is how a payment finds its invoice: right after the import, each payment
carrying the KID of an issued invoice, paid to the account that invoice printed, is
registered against it, by the person who imported the file; the rest are kept for a
person to handle. This page is the operator's side: the
agreement to ask the bank for, where the files are, the format each account is imported
in, and what can go wrong. The rules of the import are in
[the reference](/en/reference/invoices/#bank-files-and-the-exception-queue).

Vantigo takes two formats:

- **OCR giro** — the 80-character records of an OCR/KID agreement, produced by Mastercard
  Payment Services for every Norwegian bank. It carries the payments made **with a valid
  KID**, and nothing else.
- **camt.054** — the ISO 20022 notification of incoming payments (versions `.001.02` and
  `.001.08`), under an agreement for all incoming payments. It carries every credit to the
  account, with or without a KID.

camt.053, the account statement, is not enough: with a payment agreement it shows lump
sums, not who paid what.

## The bank agreement

1. **Agree a KID with the bank.** Ask for an OCR/KID agreement — "Fakturere med KID" or an
   "innbetalingsavtale" — on the account in **Invoice settings**. The bank registers a KID
   length and a check digit method (MOD10 or MOD11) with Mastercard Payment Services;
   enter exactly that on the card **KID** in **Invoice settings**
   ([the KID agreement](/en/user/invoices/#agree-a-kid-with-the-bank)), so every invoice
   prints a KID the bank accepts.
2. **Choose the file you will import.** The OCR agreement gives you OCR giro files of the
   KID payments. For every incoming payment — a customer who pays without the KID, or
   with a wrong one, included — ask instead for an eGiro / camt.054 agreement for incoming
   payments ("Innbetaling Total" at some banks), which gives camt.054 files. One of the two
   is enough per account; importing both for the same account would read each KID payment
   twice.
3. **Consider tvungen KID** — a separate option on the OCR agreement under which the bank
   refuses a payment without a KID or with an invalid one (paper giro excepted). Your
   customers then cannot pay without the KID, so the OCR file misses nothing a customer
   paid; without it, a payment without a valid KID reaches the account but **not the OCR
   file**, and only the statement or a camt.054 file shows it.

The bank's prices for these agreements differ; DNB and Nordea charge a set-up fee, a
monthly fee and a fee per KID payment.

## Getting the files

Download the file of the days you want from the online bank, then import it in Invoices.
Vantigo does not fetch files from the bank by itself in this release.

- **DNB**: the corporate online bank's file transfer offers the OCR and the eGiro files,
  and a file can be ordered again under "File transfer – Order files" (at a fee).
- **Nordea**: the online bank's "hente fil" lets you fetch the OCR file.
- **SpareBank 1**: "Meny → Filer → Hent filer".

Whether Nordea's and SpareBank 1's online banks offer camt.054 as a manual download is
**uncertain**; ask the bank. Handelsbanken was not looked into.

The same file can be imported only once, and so can a file of the same identity (OCR's
sender, transmission number and recipient; camt.054's message id and creation time) —
the second is refused naming the first import, who made it and when. A file that
overlaps one already imported — two downloads covering the same days, a copy the bank
sends again — is imported, and the payments it shares with the earlier one are kept as
duplicates of them, never read twice.

## The format of each account

Each receiving account is imported in one format. **The first file imported for an
account sets it**: an OCR giro file makes the account `ocr`, a camt.054 file `camt054`. A
later file of the other format for that account is refused with
`bank_import_format_mismatch`, naming the account and its format, and nothing of it is
imported.

To switch an account — say from OCR to camt.054 when you take the agreement for all
incoming payments — someone with `invoices:manage` changes the account's format
(`PUT /api/v1/invoices/bank-accounts/{account}/format`, until the screen for it arrives
in this release). Vantigo then records the **cutover**: the latest booking day of that
account's payments read in the old format. Matching then holds back a payment of the new
format booked on or before the cutover as a possible duplicate, left for a person,
because the old format may already have brought it in. Make the switch once the last old-format file is
imported, and start the new format's files from the day after. A change of bank is a new
account, with its own format.

`GET /api/v1/invoices/bank-accounts` lists every account with its format, the previous
format and cutover, and its latest file.

## Before you import

- **The accounts.** Every account a file names must be the seller's **Bank account** in
  **Invoice settings**, or one an issued invoice printed (so payments to an account the
  seller had before are still read); a Norwegian IBAN is read as its account number.
  Otherwise the file is refused with `bank_account_unknown`, naming the account's last
  four digits — check the settings, or that the file is this company's.
- **An object store** ([Object storage](/en/admin/object-storage/)). Every imported file is
  kept as it was uploaded, under `bank-files/<sha256>.ocr` or `.xml`, because it is the
  documentation of the payments booked from it (bokføringsloven § 10). Without a store an
  import is refused with `storage_unavailable` and nothing is read.

## What can go wrong

- **400 on the file.** It is not an OCR giro or camt.054 file, it is past 10 MiB or holds
  more than 5 000 payments, or it breaks its own rules — a control total that does not add
  up, a booking day after today, an amount not in NOK. The message names the record or
  the element. Nothing is imported: download the file again, and do not edit it by hand.
- **`bank_account_unknown`**, **`bank_file_duplicate`**, **`bank_import_format_mismatch`**:
  above.
- **What OCR leaves out.** An OCR giro file holds only payments with a valid KID; a
  customer who paid without one, or with one the bank rejected, is not in it. Without
  tvungen KID, look for such payments on the account statement, or import camt.054
  instead. Card payments (OCR's card information) are counted and ignored.
- **Payments left pending.** Matching runs right after the import, one payment at a
  time; if it stops early — a database error, or the upload's request ending — the file
  stays imported, the import's answer counts the rest as `pending`, and the stop is
  logged at warn with the file and the payment. Someone with `invoices:payments` matches
  the rest with `POST /api/v1/invoices/bank-files/{id}/match` (until the screen for it
  arrives), and is then the one registering them.
- **A negative assignment.** An OCR assignment whose payments net below zero — a reversal
  larger than the payments of the day — cannot be written in the format's end record;
  whether Mastercard Payment Services can send one at all is **uncertain**. A negative
  line within an assignment is read and kept apart for a person.
