import { describe, expect, it } from "vitest";
import { routerErrorKind } from "./router-error-utils";

describe("router error classification", () => {
  it("classifies fetch failures as offline", () => {
    expect(routerErrorKind(new TypeError("Failed to fetch"))).toBe("offline");
  });

  it("classifies forbidden API errors as forbidden", () => {
    expect(routerErrorKind(Object.assign(new Error("Forbidden"), { status: 403 }))).toBe("forbidden");
  });

  it("classifies other errors as unexpected", () => {
    expect(routerErrorKind(new Error("Unexpected failure"))).toBe("unexpected");
  });
});
