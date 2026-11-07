import { describe, expect, it } from "vitest";
import { EMAIL_DESIGN_WIDTH, fitScale } from "./conversationHelpers";

describe("fitScale", () => {
  it("leaves an e-mail alone where it fits, or before the width is known", () => {
    expect(fitScale(0)).toBe(1);
    expect(fitScale(EMAIL_DESIGN_WIDTH)).toBe(1);
    expect(fitScale(900)).toBe(1);
  });

  it("scales a 600 px e-mail down to the space it is given", () => {
    expect(fitScale(300)).toBe(0.5);
    expect(fitScale(320)).toBeCloseTo(320 / 600);
  });
});
