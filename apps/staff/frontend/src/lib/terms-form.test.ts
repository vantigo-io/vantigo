import { describe, expect, it } from "vitest";
import { emptyTerms, hoursMismatchMessage } from "./terms-form";

const t = (key: string, options?: Record<string, unknown>) => `${key} ${JSON.stringify(options ?? {})}`;
const nb = (value: number) => new Intl.NumberFormat("nb-NO").format(value);

describe("the hours mismatch sentence", () => {
  it("writes the scheme's hours as the reader writes a number", () => {
    const message = hoursMismatchMessage(
      t,
      { ...emptyTerms(), workingHoursScheme: "shift_365", fullTimeHoursPerWeek: 37.5 },
      nb,
    );
    expect(message).toBe('hoursRuleExactly {"hours":"36,5"}');
  });

  it("says nothing for hours that fit, or are left to the scheme", () => {
    expect(
      hoursMismatchMessage(t, { ...emptyTerms(), workingHoursScheme: "shift_365", fullTimeHoursPerWeek: 36.5 }, nb),
    ).toBeNull();
    expect(
      hoursMismatchMessage(t, { ...emptyTerms(), workingHoursScheme: "shift_365", fullTimeHoursPerWeek: "" }, nb),
    ).toBeNull();
  });
});
