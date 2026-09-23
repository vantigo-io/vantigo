import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ManageGroupsModal } from "./-manage-groups-modal";

const jsonResponse = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": status >= 400 ? "application/problem+json" : "application/json" },
  });

// Literally the wire shapes: Key accounts has no defaultPaymentTermsDays key at
// all, because the server omits an unset optional field rather than sending null.
const groupRows = [
  { id: "g1", name: "Retail", defaultPaymentTermsDays: 30, customerCount: 2 },
  { id: "g2", name: "Key accounts", customerCount: 0 },
];

const stubFetch = (options: { createConflict?: boolean; createInvalid?: boolean } = {}) => {
  const fetchMock = vi.fn((_input: RequestInfo | URL, init?: RequestInit) => {
    if (init?.method === "DELETE") return Promise.resolve(new Response(null, { status: 204 }));
    if (init?.method === "POST" && options.createInvalid) {
      // The problem body the server answers for a term out of range, keyed by
      // the REQUEST's field name, which is not the form's.
      return Promise.resolve(
        jsonResponse(
          { title: "Validation failed", errors: { defaultPaymentTermsDays: ["must be between 0 and 365"] } },
          400,
        ),
      );
    }
    if (init?.method === "POST") {
      return Promise.resolve(
        options.createConflict
          ? jsonResponse({ title: "Customer group already exists", code: "group_exists" }, 409)
          : jsonResponse({ id: "g3", name: "Public sector", defaultPaymentTermsDays: 45, customerCount: 0 }, 201),
      );
    }
    if (init?.method === "PUT")
      return Promise.resolve(jsonResponse({ id: "g1", name: "Retail chains", customerCount: 2 }));
    return Promise.resolve(jsonResponse(groupRows));
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
};

const renderModal = () =>
  render(
    <MantineProvider env="test">
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <ManageGroupsModal opened onClose={() => {}} />
      </QueryClientProvider>
    </MantineProvider>,
  );

describe("ManageGroupsModal", () => {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it("lists every group with its default and its member count", async () => {
    stubFetch();
    renderModal();
    await screen.findByText("Retail");
    expect(screen.getByText("30 days")).toBeInTheDocument();
    expect(screen.getByText("2 customers")).toBeInTheDocument();
    // A group with no default of its own says so rather than showing a 0.
    expect(screen.getByText("No default")).toBeInTheDocument();
    expect(screen.getByText("No customers")).toBeInTheDocument();
  });

  it("creates a group through its own POST", async () => {
    const fetchMock = stubFetch();
    renderModal();
    await screen.findByText("Retail");
    await userEvent.type(screen.getByRole("textbox", { name: "Group name" }), "Public sector");
    await userEvent.type(screen.getByRole("textbox", { name: "Default payment terms (days)" }), "45");
    await userEvent.click(screen.getByRole("button", { name: "Create group" }));

    await waitFor(() => {
      const posts = fetchMock.mock.calls.filter(([, init]) => (init as RequestInit)?.method === "POST");
      expect(posts).toHaveLength(1);
      expect(JSON.parse(String((posts[0][1] as RequestInit).body))).toEqual({
        name: "Public sector",
        defaultPaymentTermsDays: 45,
      });
    });
  });

  it("puts the 409 under the name field rather than in a notification", async () => {
    stubFetch({ createConflict: true });
    renderModal();
    await screen.findByText("Retail");
    await userEvent.type(screen.getByRole("textbox", { name: "Group name" }), "retail");
    await userEvent.click(screen.getByRole("button", { name: "Create group" }));
    expect(await screen.findByText("Another group already has that name.")).toBeInTheDocument();
  });

  it("renames a group and sends both fields, so an emptied default clears it", async () => {
    const fetchMock = stubFetch();
    renderModal();
    await userEvent.click(await screen.findByRole("button", { name: "Edit Retail" }));
    const name = screen.getByRole("textbox", { name: "Group name" });
    await userEvent.clear(name);
    await userEvent.type(name, "Retail chains");
    await userEvent.clear(screen.getByRole("textbox", { name: "Default payment terms (days)" }));
    await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

    await waitFor(() => {
      const puts = fetchMock.mock.calls.filter(([, init]) => (init as RequestInit)?.method === "PUT");
      expect(puts).toHaveLength(1);
      // A full replace: the emptied field is null, not omitted-and-kept.
      expect(JSON.parse(String((puts[0][1] as RequestInit).body))).toEqual({
        name: "Retail chains",
        defaultPaymentTermsDays: null,
      });
    });
  });

  it("refuses to delete a group with members, and says why, without asking the server", async () => {
    const fetchMock = stubFetch();
    renderModal();
    await screen.findByText("Retail");
    // The server would answer 409 group_in_use; the modal already knows the
    // count, so it does not offer the click at all.
    expect(screen.getByRole("button", { name: "Delete Retail" })).toBeDisabled();
    expect(screen.getByText("2 customers belong to Retail. Move them out before deleting it.")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Delete Key accounts" }));
    await userEvent.click(screen.getByRole("button", { name: "Delete group" }));
    await waitFor(() =>
      expect(fetchMock.mock.calls.filter(([, init]) => (init as RequestInit)?.method === "DELETE")).toHaveLength(1),
    );
  });

  it("says why a delete is refused to a screen reader too, not only on screen", async () => {
    stubFetch();
    renderModal();
    await screen.findByText("Retail");
    expect(screen.getByRole("button", { name: "Delete Retail" })).toHaveAccessibleDescription(
      "2 customers belong to Retail. Move them out before deleting it.",
    );
    // Nothing blocks an empty group's delete, so it describes nothing.
    expect(screen.getByRole("button", { name: "Delete Key accounts" })).not.toHaveAccessibleDescription();
  });

  it.each(["366", "3.5", "ten"])("refuses %s days under the field, without asking the server", async (days) => {
    const fetchMock = stubFetch();
    renderModal();
    await screen.findByText("Retail");
    await userEvent.type(screen.getByRole("textbox", { name: "Group name" }), "Public sector");
    await userEvent.type(screen.getByRole("textbox", { name: "Default payment terms (days)" }), days);
    await userEvent.click(screen.getByRole("button", { name: "Create group" }));

    expect(
      await screen.findByText("Give a whole number of days between 0 and 365, or leave it blank."),
    ).toBeInTheDocument();
    expect(fetchMock.mock.calls.filter(([, init]) => (init as RequestInit)?.method === "POST")).toHaveLength(0);
  });

  it("puts the server's term error under the days field, whose name is not the request's", async () => {
    stubFetch({ createInvalid: true });
    renderModal();
    await screen.findByText("Retail");
    await userEvent.type(screen.getByRole("textbox", { name: "Group name" }), "Public sector");
    await userEvent.type(screen.getByRole("textbox", { name: "Default payment terms (days)" }), "45");
    await userEvent.click(screen.getByRole("button", { name: "Create group" }));

    const days = screen.getByRole("textbox", { name: "Default payment terms (days)" });
    await waitFor(() => expect(days).toHaveAccessibleDescription("must be between 0 and 365"));
  });

  it("gives the create form back when the group being edited is deleted", async () => {
    // Left open, the edit form would save into a group that no longer exists
    // and earn a 404 for it.
    stubFetch();
    renderModal();
    await userEvent.click(await screen.findByRole("button", { name: "Edit Key accounts" }));
    expect(screen.getByRole("button", { name: "Save changes" })).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Delete Key accounts" }));
    await userEvent.click(screen.getByRole("button", { name: "Delete group" }));

    expect(await screen.findByRole("button", { name: "Create group" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Save changes" })).not.toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "Group name" })).toHaveValue("");
  });
});
