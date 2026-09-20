import { describe, expect, it } from "vitest";
import schema from "../api-schema.d.ts?raw";
import { groupRatesByKind, rateKinds } from "./rate-kinds";

/**
 * The contract declares `kind` as a plain string with the ten names written
 * out in its **description** — OpenAPI 3.0 without enum members — so this
 * package has to keep a list of its own, and a kind added to the contract
 * would otherwise be invisible here until somebody put a row in it. That is
 * exactly the `per_diem_overnight_other` hole this delivery closed.
 *
 * The generated `api-schema.d.ts` carries that description verbatim, so it is
 * the closest thing to the contract that lives inside the package: reading the
 * sentence back out of it makes the list derived in the only way a prose
 * description allows.
 */
const contractKinds = (): string[] => {
  const sentence = /@description One of: ([^*]+?)\.\s/.exec(schema);
  if (!sentence) throw new Error("the rate kinds' description is no longer in api-schema.d.ts");
  return sentence[1].split(",").map((kind) => kind.trim());
};

describe("rateKinds", () => {
  it("is exactly the kinds the contract names, in the contract's own order", () => {
    expect([...rateKinds]).toEqual(contractKinds());
  });

  it("gives every one of them a group, carrying rows or not", () => {
    // A kind with no rows is what an administrator fills. Two ship empty on
    // purpose — `mileage_customer` and `per_diem_overnight_other` — and a list
    // that hid them would hide the only place to price a night that is not a
    // hotel.
    const groups = groupRatesByKind([
      { id: 1, kind: "mileage", validFrom: "2026-01-01", value: 5.3, currency: "NOK", source: "State rate" },
    ]);
    expect(groups.map((group) => group.kind)).toEqual(contractKinds());
    expect(groups.find((group) => group.kind === "per_diem_overnight_other")?.rates).toEqual([]);
  });

  it("keeps a kind this build has never heard of rather than dropping its rows", () => {
    const groups = groupRatesByKind([{ id: 9, kind: "per_diem_ferry", validFrom: "2026-01-01", value: 200 }]);
    expect(groups.at(-1)).toEqual({
      kind: "per_diem_ferry",
      rates: [{ id: 9, kind: "per_diem_ferry", validFrom: "2026-01-01", value: 200 }],
    });
  });
});
