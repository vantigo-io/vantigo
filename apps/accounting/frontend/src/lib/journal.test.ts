import { describe, expect, it } from "vitest";
import type { AccountingVoucher } from "../api/accounting";
import { emptyLine, fromOre, linesFromVoucher, signedOre, toJournalInput, toOre, typedDifference } from "./journal";

describe("the journal form's arithmetic", () => {
  it("reads amounts as typed, to the øre", () => {
    expect(toOre("1 234,5")).toBe(123450n);
    expect(toOre("0.07")).toBe(7n);
    expect(toOre("12")).toBe(1200n);
    expect(toOre("1,234")).toBeUndefined();
    expect(toOre("-5")).toBeUndefined();
    expect(toOre("")).toBeUndefined();
    expect(fromOre(-123450n)).toBe("-1234.50");
    expect(fromOre(7n)).toBe("0.07");
  });

  it("signs a line debit positive and credit negative, and only with one side", () => {
    expect(signedOre({ ...emptyLine(), debit: "100" })).toBe(10000n);
    expect(signedOre({ ...emptyLine(), credit: "100" })).toBe(-10000n);
    expect(signedOre({ ...emptyLine(), debit: "1", credit: "1" })).toBeUndefined();
    expect(signedOre({ ...emptyLine(), debit: "0" })).toBeUndefined();
  });

  it("sends the contract's journal, blank lines left out", () => {
    const lines = [
      { ...emptyLine(), accountId: "1001", debit: "1250", taxCodeId: "1002", text: " Rent " },
      emptyLine(),
      { ...emptyLine(), accountId: "1003", credit: "1250", customerId: "501" },
    ];
    expect(toJournalInput("2026-03-01", " Rent ", "gross", lines)).toEqual({
      voucherDate: "2026-03-01",
      description: "Rent",
      entry: "gross",
      lines: [
        { accountId: 1001, amount: "1250.00", taxCodeId: 1002, customerId: null, projectId: null, text: "Rent" },
        { accountId: 1003, amount: "-1250.00", taxCodeId: null, customerId: 501, projectId: null, text: undefined },
      ],
    });
    expect(typedDifference(lines)).toBe(0n);
  });

  it("copies a voucher's lines without its tax lines", () => {
    const voucher = {
      postings: [
        {
          accountId: 1,
          amount: "1000.00",
          taxCodeId: 9,
          taxRole: "base",
          customerId: null,
          projectId: 4,
          description: "Rent",
        },
        {
          accountId: 2,
          amount: "250.00",
          taxCodeId: 9,
          taxRole: "tax",
          customerId: null,
          projectId: 4,
          description: "Rent",
        },
        {
          accountId: 3,
          amount: "-1250.00",
          taxCodeId: null,
          taxRole: null,
          customerId: null,
          projectId: null,
          description: "",
        },
      ],
    } as unknown as AccountingVoucher;
    const lines = linesFromVoucher(voucher);
    expect(lines.map((l) => [l.accountId, l.debit, l.credit, l.taxCodeId, l.projectId])).toEqual([
      ["1", "1000.00", "", "9", "4"],
      ["3", "", "1250.00", null, ""],
    ]);
  });
});
