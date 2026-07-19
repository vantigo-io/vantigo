import { describe, expect, it } from "vitest";
import { isPreferredCustomerType, resolveDefaultLegalType } from "./preferences";

describe("isPreferredCustomerType", () => {
  it("accepts known values", () => {
    expect(isPreferredCustomerType("business")).toBe(true);
    expect(isPreferredCustomerType("private")).toBe(true);
    expect(isPreferredCustomerType("last")).toBe(true);
  });

  it("rejects unknown values", () => {
    expect(isPreferredCustomerType("company")).toBe(false);
    expect(isPreferredCustomerType(null)).toBe(false);
    expect(isPreferredCustomerType(undefined)).toBe(false);
  });
});

describe("resolveDefaultLegalType", () => {
  it("defaults to business", () => {
    expect(resolveDefaultLegalType(null, null)).toBe("business");
    expect(resolveDefaultLegalType(undefined, undefined)).toBe("business");
    expect(resolveDefaultLegalType("business", "private")).toBe("business");
  });

  it("honors private preference", () => {
    expect(resolveDefaultLegalType("private", null)).toBe("private");
  });

  it("uses last used type when preferred is last", () => {
    expect(resolveDefaultLegalType("last", "private")).toBe("private");
    expect(resolveDefaultLegalType("last", "business")).toBe("business");
    expect(resolveDefaultLegalType("last", null)).toBe("business");
  });

  it("ignores garbage values", () => {
    expect(resolveDefaultLegalType("bogus", "private")).toBe("business");
    expect(resolveDefaultLegalType("last", "bogus")).toBe("business");
  });
});
