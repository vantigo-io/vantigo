import type { AccountingJournalInput, AccountingVoucher } from "../api/accounting";

/** One line of the journal entry form, as typed: the browser holds it until Post (A4). */
export interface FormLine {
  key: string;
  accountId: string | null;
  debit: string;
  credit: string;
  taxCodeId: string | null;
  customerId: string;
  projectId: string;
  text: string;
}

let counter = 0;

/** An empty line. */
export const emptyLine = (): FormLine => {
  counter += 1;
  return {
    key: `line-${counter}`,
    accountId: null,
    debit: "",
    credit: "",
    taxCodeId: null,
    customerId: "",
    projectId: "",
    text: "",
  };
};

const amountPattern = /^\d{1,12}([.,]\d{1,2})?$/;

/** An amount as typed (`1 234,5`) in øre, or undefined when it is not one. */
export const toOre = (text: string): bigint | undefined => {
  const clean = text.replace(/[\s\u00a0]/g, "");
  if (clean === "" || !amountPattern.test(clean)) return undefined;
  const [whole, fraction = ""] = clean.replace(",", ".").split(".");
  return BigInt(whole) * 100n + BigInt(fraction.padEnd(2, "0"));
};

/** øre as the contract's exact decimal text: `-1234.50`. */
export const fromOre = (ore: bigint): string => {
  const negative = ore < 0n;
  const abs = negative ? -ore : ore;
  const whole = abs / 100n;
  const fraction = (abs % 100n).toString().padStart(2, "0");
  return `${negative ? "-" : ""}${whole}.${fraction}`;
};

/** A line's signed amount in øre — debit positive, credit negative — or undefined when it has none or both. */
export const signedOre = (line: FormLine): bigint | undefined => {
  const debit = line.debit.trim() === "" ? undefined : toOre(line.debit);
  const credit = line.credit.trim() === "" ? undefined : toOre(line.credit);
  if (debit !== undefined && credit === undefined && debit !== 0n) return debit;
  if (credit !== undefined && debit === undefined && credit !== 0n) return -credit;
  return undefined;
};

/** Whether a line has anything typed in it. */
export const lineIsBlank = (line: FormLine) =>
  line.accountId === null && line.debit.trim() === "" && line.credit.trim() === "" && line.text.trim() === "";

const optionalNumber = (text: string | null): number | undefined => {
  if (text === null || text.trim() === "") return undefined;
  const value = Number(text);
  return Number.isInteger(value) && value > 0 ? value : undefined;
};

/** The form as the contract's journal (A6): blank lines left out. */
export const toJournalInput = (
  voucherDate: string,
  description: string,
  entry: "net" | "gross",
  lines: readonly FormLine[],
): AccountingJournalInput => ({
  voucherDate,
  description: description.trim(),
  entry,
  lines: lines
    .filter((line) => !lineIsBlank(line))
    .map((line) => {
      const ore = signedOre(line);
      return {
        accountId: optionalNumber(line.accountId) ?? 0,
        amount: ore === undefined ? "" : fromOre(ore),
        taxCodeId: optionalNumber(line.taxCodeId) ?? null,
        customerId: optionalNumber(line.customerId) ?? null,
        projectId: optionalNumber(line.projectId) ?? null,
        text: line.text.trim() || undefined,
      };
    }),
});

/** What the typed lines sum to, before any tax line, in øre: zero when they balance on their own. */
export const typedDifference = (lines: readonly FormLine[]): bigint =>
  lines.reduce((sum, line) => sum + (signedOre(line) ?? 0n), 0n);

/**
 * A posted voucher's lines for the form, for Copy from a voucher (R13): every
 * posting but its tax lines, which the form computes again, entered net.
 */
export const linesFromVoucher = (voucher: AccountingVoucher): FormLine[] =>
  voucher.postings
    .filter((posting) => posting.taxRole !== "tax")
    .map((posting) => {
      const ore = toOre(posting.amount.replace("-", "")) ?? 0n;
      const credit = posting.amount.startsWith("-");
      return {
        ...emptyLine(),
        accountId: String(posting.accountId),
        debit: credit ? "" : fromOre(ore),
        credit: credit ? fromOre(ore) : "",
        taxCodeId: posting.taxCodeId === null ? null : String(posting.taxCodeId),
        customerId: posting.customerId === null ? "" : String(posting.customerId),
        projectId: posting.projectId === null ? "" : String(posting.projectId),
        text: posting.description,
      };
    });
