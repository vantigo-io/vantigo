import { describe, expect, it } from "vitest";
import { stubFetch } from "../test/fetch";
import {
  createMilestone,
  deleteMilestone,
  milestonePlanQueryOptions,
  moveMilestone,
  setMilestoneStatus,
  updateMilestone,
} from "./milestones";
import { ApiValidationError } from "./request";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const runQuery = (options: { queryFn?: unknown }) =>
  (options.queryFn as (context: unknown) => Promise<unknown>)({ signal: undefined });

describe("milestonePlanQueryOptions", () => {
  it("reads the project's invoice plan under the milestones key", async () => {
    const plan = {
      milestones: [{ id: 1, name: "Kick-off" }],
      totals: { planned: 1, ready: 0, invoiced: 0 },
    };
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, plan)));

    const options = milestonePlanQueryOptions(7);
    const result = await runQuery(options);

    expect(result).toEqual(plan);
    expect(options.queryKey).toEqual(["projects", "milestones", "plan", 7]);
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects/7/milestones", { signal: undefined });
  });

  it("surfaces the 403 a caller without financial rights is answered with", async () => {
    stubFetch(() => Promise.resolve(jsonResponse(403, { message: "Forbidden" })));

    const error = await runQuery(milestonePlanQueryOptions(7)).catch((e: unknown) => e as { status?: number });

    expect(error).toMatchObject({ status: 403 });
  });
});

describe("createMilestone", () => {
  it("POSTs the milestone to the project", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(201, { id: 1 })));
    const input = { name: "Kick-off", amount: 100000 };

    await createMilestone(7, input);

    expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects/7/milestones", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input),
    });
  });

  it("reports the 'exactly one of amount and percent' rule on both fields", async () => {
    stubFetch(() =>
      Promise.resolve(
        jsonResponse(400, {
          title: "Invalid project",
          errors: {
            amount: ["Exactly one of amount and percent is required"],
            percent: ["Exactly one of amount and percent is required"],
          },
        }),
      ),
    );

    const error = await createMilestone(7, { name: "Kick-off" }).catch((e: unknown) => e);

    expect(error).toBeInstanceOf(ApiValidationError);
    expect((error as ApiValidationError).fieldErrors).toEqual({
      amount: "Exactly one of amount and percent is required",
      percent: "Exactly one of amount and percent is required",
    });
  });
});

describe("updateMilestone", () => {
  it("PUTs the whole milestone with the revision it was read at", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, { id: 12 })));
    const input = { name: "Kick-off", percent: 30, revision: 4 };

    await updateMilestone(12, input);

    expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects/milestones/12", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input),
    });
  });
});

describe("deleteMilestone", () => {
  it("DELETEs the milestone", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(new Response(null, { status: 204 })));

    await deleteMilestone(12);

    expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects/milestones/12", { method: "DELETE" });
  });
});

describe("moveMilestone", () => {
  it("PUTs where the milestone should sit, carrying the revision", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, { id: 12 })));

    await moveMilestone(12, { position: 2, revision: 4 });

    expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects/milestones/12/position", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ position: 2, revision: 4 }),
    });
  });
});

describe("setMilestoneStatus", () => {
  it("POSTs the move, with the invoice fields only when they are given", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, { id: 12 })));

    await setMilestoneStatus(12, { status: "ready", revision: 4 });
    expect(fetchMock).toHaveBeenNthCalledWith(1, "/api/v1/projects/milestones/12/status", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ status: "ready", revision: 4 }),
    });

    await setMilestoneStatus(12, {
      status: "invoiced",
      revision: 5,
      invoiceReference: "2026-0042",
      invoiceDate: "2026-03-31",
    });
    expect(fetchMock).toHaveBeenNthCalledWith(2, "/api/v1/projects/milestones/12/status", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        status: "invoiced",
        revision: 5,
        invoiceReference: "2026-0042",
        invoiceDate: "2026-03-31",
      }),
    });
  });

  it("carries a refused move back on the status field", async () => {
    stubFetch(() =>
      Promise.resolve(
        jsonResponse(400, {
          title: "Invalid project",
          errors: { status: ["A milestone cannot move from 'cancelled' to 'ready'"] },
        }),
      ),
    );

    const error = await setMilestoneStatus(12, { status: "ready", revision: 4 }).catch((e: unknown) => e);

    expect(error).toBeInstanceOf(ApiValidationError);
    expect((error as ApiValidationError).fieldErrors.status).toBe(
      "A milestone cannot move from 'cancelled' to 'ready'",
    );
  });
});
