import { describe, expect, it } from "vitest";
import { stubFetch } from "../test/fetch";
import {
  createDeliveryMilestone,
  deleteDeliveryMilestone,
  deliveryMilestoneQueryOptions,
  deliveryMilestonesQueryOptions,
  moveDeliveryMilestone,
  setDeliveryMilestoneStatus,
  updateDeliveryMilestone,
} from "./delivery-milestones";
import { ApiValidationError } from "./request";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const runQuery = (options: { queryFn?: unknown }) =>
  (options.queryFn as (context: unknown) => Promise<unknown>)({ signal: undefined });

describe("deliveryMilestonesQueryOptions", () => {
  it("reads the project's delivery milestones", async () => {
    const list = {
      milestones: [
        {
          id: 1,
          projectId: 7,
          name: "MVP",
          position: 1,
          status: "open",
          totalTasks: 2,
          completedTasks: 1,
          progressPercent: 50,
          revision: 1,
          createdAt: "2026-10-31T00:00:00Z",
          updatedAt: "2026-10-31T00:00:00Z",
        },
      ],
    };
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, list)));

    const options = deliveryMilestonesQueryOptions(7);
    const result = await runQuery(options);

    expect(result).toEqual(list);
    expect(options.queryKey).toEqual(["projects", "delivery-milestones", "list", 7]);
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects/7/delivery-milestones", { signal: undefined });
  });
});

describe("deliveryMilestoneQueryOptions", () => {
  it("reads a single delivery milestone", async () => {
    const milestone = {
      id: 10,
      projectId: 7,
      name: "Phase 1",
      position: 1,
      status: "open",
      totalTasks: 0,
      completedTasks: 0,
      progressPercent: 0,
      revision: 1,
      createdAt: "2026-10-31T00:00:00Z",
      updatedAt: "2026-10-31T00:00:00Z",
    };
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, milestone)));

    const options = deliveryMilestoneQueryOptions(10);
    const result = await runQuery(options);

    expect(result).toEqual(milestone);
    expect(options.queryKey).toEqual(["projects", "delivery-milestones", "detail", 10]);
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects/delivery-milestones/10", { signal: undefined });
  });
});

describe("createDeliveryMilestone", () => {
  it("POSTs the delivery milestone to the project", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(201, { id: 1, name: "Release 1" })));
    const input = { name: "Release 1", targetDate: "2026-12-01" };

    await createDeliveryMilestone(7, input);

    expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects/7/delivery-milestones", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input),
    });
  });

  it("handles validation error", async () => {
    stubFetch(() =>
      Promise.resolve(
        jsonResponse(400, {
          title: "Invalid milestone",
          errors: {
            name: ["Name cannot be blank"],
          },
        }),
      ),
    );

    const error = await createDeliveryMilestone(7, { name: "" }).catch((e: unknown) => e);

    expect(error).toBeInstanceOf(ApiValidationError);
    expect((error as ApiValidationError).fieldErrors).toEqual({
      name: "Name cannot be blank",
    });
  });
});

describe("updateDeliveryMilestone", () => {
  it("PUTs the delivery milestone update", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, { id: 1, name: "Updated MVP" })));
    const input = { name: "Updated MVP", revision: 1 };

    await updateDeliveryMilestone(1, input);

    expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects/delivery-milestones/1", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input),
    });
  });
});

describe("deleteDeliveryMilestone", () => {
  it("DELETEs the milestone", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(new Response(null, { status: 204 })));

    await deleteDeliveryMilestone(1);

    expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects/delivery-milestones/1", {
      method: "DELETE",
    });
  });
});

describe("moveDeliveryMilestone", () => {
  it("PUTs position", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, { id: 1, position: 2 })));
    const input = { position: 2, revision: 1 };

    await moveDeliveryMilestone(1, input);

    expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects/delivery-milestones/1/position", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input),
    });
  });
});

describe("setDeliveryMilestoneStatus", () => {
  it("POSTs status transition", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, { id: 1, status: "completed" })));
    const input = { status: "completed" as const, revision: 1 };

    await setDeliveryMilestoneStatus(1, input);

    expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects/delivery-milestones/1/status", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input),
    });
  });
});
