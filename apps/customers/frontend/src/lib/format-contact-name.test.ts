import { describe, expect, it } from "vitest";

import { formatContactName } from "./format-contact-name";

describe("formatContactName", () => {
  it("composes all parts in western order", () => {
    expect(
      formatContactName({
        prefix: "Dr.",
        firstName: "Anders",
        middleName: "Bernhard",
        lastName: "Refsdal",
        suffix: "PhD",
      }),
    ).toBe("Dr. Anders Bernhard Refsdal PhD");
  });

  it("skips missing parts", () => {
    expect(
      formatContactName({
        prefix: null,
        firstName: "Anders",
        middleName: null,
        lastName: "Refsdal",
        suffix: null,
      }),
    ).toBe("Anders Refsdal");
  });
});
