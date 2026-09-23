import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ManageTagsModal } from "./-manage-tags-modal";

const jsonResponse = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const tagRows = [
  { id: "t1", name: "VIP", color: "grape", customerCount: 2 },
  { id: "t2", name: "Prospect", color: null, customerCount: 0 },
];

const stubFetch = () =>
  vi.stubGlobal(
    "fetch",
    vi.fn((_input: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === "DELETE") return Promise.resolve(new Response(null, { status: 204 }));
      if (init?.method === "PUT" || init?.method === "POST") {
        return Promise.resolve(jsonResponse({ id: "t1", name: "Key account", color: "teal", customerCount: 2 }));
      }
      return Promise.resolve(jsonResponse(tagRows));
    }),
  );

const renderModal = () =>
  render(
    <MantineProvider env="test">
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <ManageTagsModal opened onClose={() => {}} />
      </QueryClientProvider>
    </MantineProvider>,
  );

describe("ManageTagsModal", () => {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it("lists every tag with how many customers carry it", async () => {
    stubFetch();
    renderModal();
    await screen.findByText("VIP");
    expect(screen.getByText("2 customers")).toBeInTheDocument();
    expect(screen.getByText("No customers")).toBeInTheDocument();
  });

  it("renames a tag through its own PUT", async () => {
    stubFetch();
    renderModal();
    await userEvent.click(await screen.findByRole("button", { name: "Rename VIP" }));
    const input = screen.getByRole("textbox", { name: "Tag name" });
    await userEvent.clear(input);
    await userEvent.type(input, "Key account");
    await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

    await waitFor(() => {
      const calls = (fetch as unknown as { mock: { calls: [unknown, RequestInit?][] } }).mock.calls;
      const put = calls.find(([url, init]) => init?.method === "PUT" && String(url) === "/api/v1/customers/tags/t1");
      expect(put).toBeDefined();
      expect(JSON.parse(String(put?.[1]?.body))).toEqual({ name: "Key account", color: "grape" });
    });
  });

  it("says how many customers a delete will affect, and only deletes on confirmation", async () => {
    stubFetch();
    renderModal();
    await userEvent.click(await screen.findByRole("button", { name: "Delete VIP" }));
    expect(screen.getByText("VIP is on 2 customers. Deleting it removes it from all of them.")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Delete tag" }));

    await waitFor(() => {
      const calls = (fetch as unknown as { mock: { calls: [unknown, RequestInit?][] } }).mock.calls;
      expect(
        calls.some(([url, init]) => init?.method === "DELETE" && String(url) === "/api/v1/customers/tags/t1"),
      ).toBe(true);
    });
  });

  it("shows the server's field error when a name is refused", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn((_input: RequestInfo | URL, init?: RequestInit) => {
        if (init?.method === "PUT") {
          return Promise.resolve(
            new Response(JSON.stringify({ errors: { name: ["A tag name cannot be null or empty"] } }), {
              status: 400,
              headers: { "Content-Type": "application/problem+json" },
            }),
          );
        }
        return Promise.resolve(jsonResponse(tagRows));
      }),
    );
    renderModal();
    await userEvent.click(await screen.findByRole("button", { name: "Rename VIP" }));
    await userEvent.click(screen.getByRole("button", { name: "Save changes" }));
    expect(await screen.findByText("A tag name cannot be null or empty")).toBeInTheDocument();
  });
});
