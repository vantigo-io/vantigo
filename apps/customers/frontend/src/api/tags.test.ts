import { afterEach, describe, expect, it, vi } from "vitest";
import { createTag, customerTagsQueryOptions, deleteTag, setCustomerTags, updateTag } from "./tags";

const jsonResponse = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const stubFetch = (respond: (url: string, init?: RequestInit) => Response) => {
  const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) =>
    Promise.resolve(respond(String(input), init)),
  );
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
};

describe("tags api", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("lists tags with their customer counts", async () => {
    stubFetch(() => jsonResponse([{ id: "t1", name: "VIP", color: "grape", customerCount: 3 }]));
    const options = customerTagsQueryOptions();
    expect(options.queryKey).toEqual(["customers", "tags"]);
    await expect(options.queryFn?.({} as never)).resolves.toEqual([
      { id: "t1", name: "VIP", color: "grape", customerCount: 3 },
    ]);
  });

  it("normalises a tag whose colour the wire omits", async () => {
    // color is nullable and omitempty on the wire, so a tag with no colour
    // arrives without the key at all. Absent and null mean the same thing, and
    // the boundary is where that is decided — every component downstream reads
    // `color: string | null`.
    stubFetch(() => jsonResponse([{ id: "t2", name: "Prospect", customerCount: 0 }]));
    const tags = (await customerTagsQueryOptions().queryFn?.({} as never)) as { color: string | null }[];
    expect(tags[0].color).toBeNull();
  });

  it("creates, renames and deletes a tag on its own URLs", async () => {
    const fetchMock = stubFetch((_url, init) => {
      if (init?.method === "DELETE") return new Response(null, { status: 204 });
      return jsonResponse({ id: "t1", name: "Key account", color: "teal", customerCount: 1 });
    });
    await createTag({ name: "Key account", color: "teal" });
    await updateTag("t1", { name: "Key account", color: null });
    await deleteTag("t1");

    const calls = fetchMock.mock.calls.map(([url, init]) => `${(init as RequestInit)?.method} ${String(url)}`);
    expect(calls).toEqual([
      "POST /api/v1/customers/tags",
      "PUT /api/v1/customers/tags/t1",
      "DELETE /api/v1/customers/tags/t1",
    ]);
    const renameBody = JSON.parse(String((fetchMock.mock.calls[1][1] as RequestInit).body));
    expect(renameBody).toEqual({ name: "Key account", color: null });
  });

  it("replaces a customer's tag set in one call", async () => {
    const fetchMock = stubFetch(() => jsonResponse({ tags: [{ id: "t1", name: "VIP", color: null }] }));
    await expect(setCustomerTags(1001, ["t1"])).resolves.toEqual({ tags: [{ id: "t1", name: "VIP", color: null }] });
    const [url, init] = fetchMock.mock.calls[0];
    expect(String(url)).toBe("/api/v1/customers/1001/tags");
    expect((init as RequestInit).method).toBe("PUT");
    expect(JSON.parse(String((init as RequestInit).body))).toEqual({ tagIds: ["t1"] });
  });
});
