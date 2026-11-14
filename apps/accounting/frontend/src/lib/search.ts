import type { AccountingVatPart } from "../api/accounting";

/** The journal entry page's search: `copy` names a posted voucher to start from (R13). */
export interface JournalSearch {
  copy?: number;
}

export const validateJournalSearch = (search: Record<string, unknown>): JournalSearch => {
  const copy = Number(search.copy);
  return Number.isInteger(copy) && copy > 0 ? { copy } : {};
};

/** The VAT drill-down's search (A21): the period, and a tax code and a part, or the unreconciled postings. */
export interface VatPostingsSearch {
  from?: string;
  to?: string;
  taxCodeId?: number;
  part?: AccountingVatPart;
  unreconciled?: boolean;
}

const vatParts: readonly AccountingVatPart[] = [
  "base",
  "output",
  "calculated",
  "deducted",
  "not_deducted",
  "loss",
  "loss_base",
  "untaxed",
  "other",
];
const isDate = (v: unknown): v is string => typeof v === "string" && /^\d{4}-\d{2}-\d{2}$/.test(v);

export const validateVatPostingsSearch = (search: Record<string, unknown>): VatPostingsSearch => {
  const out: VatPostingsSearch = {};
  if (isDate(search.from)) out.from = search.from;
  if (isDate(search.to)) out.to = search.to;
  const code = Number(search.taxCodeId);
  if (Number.isInteger(code) && code > 0) out.taxCodeId = code;
  if (vatParts.includes(search.part as AccountingVatPart)) out.part = search.part as AccountingVatPart;
  if (search.unreconciled === true || search.unreconciled === "true") out.unreconciled = true;
  return out;
};
