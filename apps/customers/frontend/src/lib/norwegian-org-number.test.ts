import { describe, expect, it } from "vitest";
import { validNorwegianOrgNumber } from "./norwegian-org-number";

describe("validNorwegianOrgNumber", () => {
  it.each([
    ["923609016", true],
    ["974760673", true],
    // One digit off the first: the check digit no longer matches.
    ["923609017", false],
    // Eight digits weighted 3 2 7 6 5 4 3 2 sum to 12, a remainder of 1, so
    // the check digit would have to be 10 — no such number exists.
    ["400000000", false],
    ["92360901", false],
    ["9236090161", false],
    ["92360901a", false],
    ["923 609 016", false],
    ["", false],
  ])("%s is %s", (value, expected) => {
    expect(validNorwegianOrgNumber(value)).toBe(expected);
  });
});
