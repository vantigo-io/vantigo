import { describe, expect, it } from "vitest";
import { ApiConflictError } from "../api/request";
import { customerWriteErrorMessage, isCustomerMerged, isCustomerReadOnly } from "./customer-write-error";

const t = (key: string) => `t:${key}`;

// Literally the problem the server answers a write on a merged-away customer.
const merged = new ApiConflictError("#5 Acme Norge AS was merged into #2 Acme AS.", {
  title: "Customer was merged",
  detail: "#5 Acme Norge AS was merged into #2 Acme AS.",
  code: "customer_merged",
  problem: {
    title: "Customer was merged",
    status: 409,
    code: "customer_merged",
    detail: "#5 Acme Norge AS was merged into #2 Acme AS.",
  },
});

describe("customerWriteErrorMessage", () => {
  it("says a merged-away customer was merged, in the catalog's words", () => {
    expect(isCustomerMerged(merged)).toBe(true);
    expect(customerWriteErrorMessage(merged, t)).toBe("t:customerMergedMessage");
  });

  it("keeps the server's own words for every other failure, a revision conflict and other codes included", () => {
    const stale = new ApiConflictError("Customer revision conflict", {
      title: "Customer revision conflict",
      problem: { title: "Customer revision conflict", status: 409 },
    });
    const duplicate = new ApiConflictError("Duplicate legal identity", {
      title: "Duplicate legal identity",
      code: "duplicate_legal_identity",
      problem: { title: "Duplicate legal identity", status: 409, code: "duplicate_legal_identity" },
    });
    for (const error of [stale, duplicate, new Error("Request failed (HTTP 500)")]) {
      expect(isCustomerMerged(error)).toBe(false);
      expect(customerWriteErrorMessage(error, t)).toBe(error.message);
    }
  });
});

// Literally the problem the server answers a write on an anonymised customer.
const anonymised = new ApiConflictError("This customer's personal data was anonymised on 2027-01-31.", {
  title: "Customer was anonymised",
  code: "customer_anonymised",
  problem: { title: "Customer was anonymised", status: 409, code: "customer_anonymised" },
});

describe("an anonymised customer's refusal", () => {
  it("is read-only, not merged, and says so in the catalog's words", () => {
    expect(isCustomerReadOnly(anonymised)).toBe(true);
    expect(isCustomerMerged(anonymised)).toBe(false);
    expect(isCustomerReadOnly(merged)).toBe(true);
    expect(customerWriteErrorMessage(anonymised, t)).toBe("t:customerAnonymisedMessage");
  });
});
