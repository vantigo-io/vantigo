import { describe, expect, it } from "vitest";
import { stubFetch } from "../test/fetch";
import { assignableUsersQueryOptions, projectRolesQueryOptions, removeProjectRole, setProjectRole } from "./people";
import { NotFoundError } from "./request";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const runQuery = (options: { queryFn?: unknown }) =>
  (options.queryFn as (context: unknown) => Promise<unknown>)({ signal: undefined });

const userId = "4f6f5a3e-0f0a-4a5e-9f6f-1f0a4a5e9f6f";

describe("projectRolesQueryOptions", () => {
  it("reads the project's roles", async () => {
    const roles = [{ userId, displayName: "Kari", role: "manager", active: true, createdAt: "2026-09-01T09:00:00Z" }];
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, roles)));

    const options = projectRolesQueryOptions(7);
    const result = await runQuery(options);

    expect(result).toEqual(roles);
    expect(options.queryKey).toEqual(["projects", "detail", 7, "roles"]);
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects/7/roles", { signal: undefined });
  });

  it("throws NotFoundError when the project does not exist", async () => {
    stubFetch(() => Promise.resolve(new Response(null, { status: 404 })));

    await expect(runQuery(projectRolesQueryOptions(999999))).rejects.toBeInstanceOf(NotFoundError);
  });
});

describe("assignableUsersQueryOptions", () => {
  it("leaves an empty search out of the query string", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, [])));

    const options = assignableUsersQueryOptions(7, "  ");
    await runQuery(options);

    expect(options.queryKey).toEqual(["projects", "detail", 7, "assignable-users", ""]);
    expect(fetchMock.actualCalls[0]?.[0]).toBe("/api/v1/projects/7/assignable-users");
  });

  it("sends the trimmed search term", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, [])));

    const options = assignableUsersQueryOptions(7, " kari ");
    await runQuery(options);

    expect(options.queryKey).toEqual(["projects", "detail", 7, "assignable-users", "kari"]);
    expect(fetchMock.actualCalls[0]?.[0]).toBe("/api/v1/projects/7/assignable-users?search=kari");
  });
});

describe("setProjectRole", () => {
  it("PUTs the role for one user", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, { userId, role: "member" })));

    await setProjectRole(7, userId, "member");

    expect(fetchMock).toHaveBeenCalledWith(`/api/v1/projects/7/roles/${userId}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ role: "member" }),
    });
  });
});

describe("removeProjectRole", () => {
  it("DELETEs the assignment and tolerates the empty 204 body", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(new Response(null, { status: 204 })));

    await expect(removeProjectRole(7, userId)).resolves.toBeUndefined();
    expect(fetchMock).toHaveBeenCalledWith(`/api/v1/projects/7/roles/${userId}`, { method: "DELETE" });
  });
});
