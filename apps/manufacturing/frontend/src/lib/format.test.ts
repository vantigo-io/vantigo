import { describe, expect, it } from "vitest";
import { refusalOf } from "../api/request";
import { refusalMessage } from "./format";

/** A t that shows the key and its values, so the words chosen are visible. */
const t = ((key: string, values?: Record<string, unknown>) =>
  values ? `${key} ${JSON.stringify(values)}` : key) as unknown as Parameters<typeof refusalMessage>[0];

const refused = (body: unknown) =>
  refusalOf(
    new Response(JSON.stringify(body), { status: 409, headers: { "Content-Type": "application/problem+json" } }),
  );

describe("refusalMessage", () => {
  it("names the checks a production, a finish or a close is missing", async () => {
    const error = await refused({
      title: "Checks are missing",
      code: "checks_incomplete",
      checks: [
        {
          checkId: 1,
          title: "Read the recipe",
          frequency: "each_work_order",
          workOrderId: 40,
          workOrderName: "Mix",
          workOrderSequence: 1,
        },
        {
          checkId: 2,
          title: "Fill weight",
          frequency: "first_piece",
          workOrderId: 41,
          workOrderName: "Fill",
          workOrderSequence: 2,
        },
      ],
    });
    expect(refusalMessage(t, error)).toBe(
      `refusal.checks_incomplete_named ${JSON.stringify({ checks: "1 · Mix: Read the recipe; 2 · Fill: Fill weight" })}`,
    );
  });

  it("says the code's words when the refusal names no checks", async () => {
    const error = await refused({ title: "Checks are missing", code: "checks_incomplete" });
    expect(refusalMessage(t, error)).toBe("refusal.checks_incomplete");
  });
});
