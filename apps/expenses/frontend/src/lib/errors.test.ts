import { describe, expect, it } from "vitest";
import { ApiValidationError } from "../api/request";
import { lineNamedIn, refusalMessage, refusalMessages, refusalsByEntry, refusalsByUnit } from "./errors";

describe("refusalsByEntry", () => {
  it("puts each per-id sentence against the expense it names", () => {
    const error = new ApiValidationError("Invalid submission", {
      entryIds: [
        "Expense 7 was not found",
        "Expense 9 needs a receipt: an outlay the employee paid for more than 1250.00 cannot be submitted without one",
      ],
    });
    const { byEntry, rest } = refusalsByEntry(error);
    expect(byEntry.get(7)).toEqual(["Expense 7 was not found"]);
    expect(byEntry.get(9)?.[0]).toContain("needs a receipt");
    expect(rest).toEqual([]);
  });

  it("keeps a body-level refusal out of the rows, where no row could show it", () => {
    const error = new ApiValidationError("Invalid submission", {
      entryIds: ["At most 500 expenses may be given at once; 501 were given"],
    });
    expect(refusalsByEntry(error).byEntry.size).toBe(0);
    expect(refusalsByEntry(error).rest).toHaveLength(1);
  });

  it("falls back to the error's own message when nothing named a field", () => {
    expect(refusalsByEntry(new Error("The server is down")).rest).toEqual(["The server is down"]);
  });
});

describe("refusalsByUnit", () => {
  it("reads both lists a batch refuses on, and keeps each unit's sentence on its own unit", () => {
    // The two units number independently, so a bare id would collide: the
    // expense refusals arrive on `entryIds` and the trips' on `claimIds`, and
    // a client that read only the first dropped every trip's explanation.
    const error = new ApiValidationError("Invalid submission", {
      entryIds: ["Expense 7 was not found"],
      claimIds: ["Travel claim 7 holds no expenses, so there is nothing to submit"],
    });
    const { byEntry, byClaim, rest } = refusalsByUnit(error);
    expect(byEntry.get(7)).toEqual(["Expense 7 was not found"]);
    expect(byClaim.get(7)).toEqual(["Travel claim 7 holds no expenses, so there is nothing to submit"]);
    expect(rest).toEqual([]);
  });

  it("reads a trip's refusal even when the expense list is absent altogether", () => {
    const error = new ApiValidationError("Invalid submission", {
      claimIds: ["Travel claim 12 is not yours"],
    });
    expect(refusalsByUnit(error).byClaim.get(12)).toEqual(["Travel claim 12 is not yours"]);
    expect(refusalsByUnit(error).rest).toEqual([]);
  });
});

describe("refusalMessages", () => {
  it("merges the two lists so a trip's refusal is never dropped", () => {
    const error = new ApiValidationError("Invalid submission", {
      entryIds: ["Expense 7 was not found"],
      claimIds: ["Travel claim 9 was not found"],
    });
    expect(refusalMessages(error)).toEqual(["Expense 7 was not found", "Travel claim 9 was not found"]);
  });
});

describe("lineNamedIn", () => {
  it("finds the line a trip's refusal points at", () => {
    expect(
      lineNamedIn("Travel claim 7 cannot be submitted: Expense 12 cannot be priced: No per_diem_6_12 rate applies"),
    ).toBe(12);
  });

  it("names nothing when the sentence is about the trip alone", () => {
    expect(lineNamedIn("Travel claim 7 holds no expenses, so there is nothing to submit")).toBeUndefined();
  });

  // The module writes the word mid-sentence here, in lower case. This is the
  // one refusal the approver's drawer exists to put against a line, so the
  // match cannot be case-sensitive.
  it("finds the line an unapprove refusal names in lower case", () => {
    expect(lineNamedIn("Travel claim 7 holds expense 12, which has been invoiced")).toBe(12);
  });
});

describe("refusalMessage", () => {
  it("prefers the field the caller is looking at", () => {
    const error = new ApiValidationError("Invalid expense", {
      entryDate: ["No mileage rate applies on this date"],
      description: ["A description holds at most 500 characters"],
    });
    expect(refusalMessage(error, "entryDate")).toBe("No mileage rate applies on this date");
    expect(refusalMessage(error)).toBe("No mileage rate applies on this date");
  });
});
