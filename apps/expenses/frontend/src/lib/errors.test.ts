import { describe, expect, it } from "vitest";
import { ApiValidationError } from "../api/request";
import { refusalMessage, refusalsByEntry } from "./errors";

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
