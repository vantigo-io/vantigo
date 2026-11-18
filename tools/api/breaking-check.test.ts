import { describe, expect, it } from "bun:test";
import { type Commit, commitsFrom, declared, type Finding, judge, operationOf } from "./breaking-check";

const removedField: Finding = {
  id: "response-property-removed",
  level: 3,
  operation: "GET",
  operationId: "getCustomers",
  path: "/api/v1/customers",
};
const removedOperation: Finding = {
  id: "api-removed-without-deprecation",
  level: 3,
  operation: "POST",
  operationId: "postCustomers",
  path: "/api/v1/customers",
};
const warning: Finding = { id: "response-optional-property-removed", level: 2, operationId: "getProducts" };

const commit = (subject: string, body = ""): Commit => ({ sha: "abc", subject, body });

describe("the breaking-change check", () => {
  it("fails a removed field no commit declares", () => {
    const verdict = judge([removedField], [commit("feat(customers): drop the legacy field")]);
    expect(verdict.undeclared).toEqual(["getCustomers"]);
  });

  it("passes a `!` commit with the trailer for the operation", () => {
    const verdict = judge(
      [removedField],
      [commit("feat(customers)!: drop the legacy field", "API-Breaking: getCustomers — the field was never filled")],
    );
    expect(verdict).toEqual({ breaking: ["getCustomers"], undeclared: [] });
  });

  it("needs the `!` as well as the trailer", () => {
    const verdict = judge([removedField], [commit("feat(customers): drop it", "API-Breaking: getCustomers — gone")]);
    expect(verdict.undeclared).toEqual(["getCustomers"]);
  });

  it("needs a trailer for every operation, and a reason in each", () => {
    const verdict = judge(
      [removedField, removedOperation],
      [commit("feat(customers)!: drop it", "API-Breaking: getCustomers — gone\nAPI-Breaking: postCustomers")],
    );
    expect(verdict.undeclared).toEqual(["postCustomers"]);
  });

  it("ignores warnings", () => {
    expect(judge([warning], [])).toEqual({ breaking: [], undeclared: [] });
  });

  it("reads a breaking subject without a scope and an ASCII dash", () => {
    expect(declared(commit("feat!: remove", "API-Breaking: postCustomers - superseded"))).toEqual(["postCustomers"]);
  });

  it("names an operation without an id by its method and path", () => {
    expect(operationOf({ id: "x", level: 3, operation: "GET", path: "/api/v1/x" })).toBe("GET /api/v1/x");
  });

  it("reads the commits gh pr view answers", () => {
    const commits = commitsFrom([
      {
        oid: "1",
        messageHeadline: "feat(orders)!: rename the total",
        messageBody: "Why.\n\nAPI-Breaking: getOrders — renamed\n\nCo-Authored-By: X <x@example.com>",
      },
    ]);
    const verdict = judge([{ ...removedField, operationId: "getOrders" }], commits);
    expect(verdict.undeclared).toEqual([]);
  });
});
