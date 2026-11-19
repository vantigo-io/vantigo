import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
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
  {
    id: "g1",
    name: "Retail",
    defaultPaymentTermsDays: 30,
    customerCount: 2,
    brand: { id: "b1", name: "Nordlys", state: "published" },
  },
  { id: "g2", name: "Key accounts", customerCount: 0 },
  {
    id: "g4",
    name: "Archived brand group",
    customerCount: 0,
    brand: { id: "b9", name: "Old brand", state: "archived" },
  },
];

// The published brands the picker offers (GET /customers/brands).
const brandRows = [
  { id: "b0", name: "Vantigo Demo AS", isDefault: true },
  { id: "b1", name: "Nordlys", isDefault: false },
  { id: "b2", name: "Fjordkraft Demo", isDefault: false },
];

const stubFetch = (
  options: { createConflict?: boolean; createInvalid?: boolean; deleteInUse?: boolean; list?: "hang" | "fail" } = {},
) => {
  const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    if (!init?.method && String(input) === "/api/v1/customers/brands") return Promise.resolve(jsonResponse(brandRows));
    if (init?.method === "DELETE")
      return Promise.resolve(
        options.deleteInUse
          ? // Somebody moved a customer in between the list and the click.
            jsonResponse({ title: "Customer group in use", status: 409, code: "group_in_use" }, 409)
          : new Response(null, { status: 204 }),
      );
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
    if (options.list === "hang") return new Promise<Response>(() => {});
    if (options.list === "fail") return Promise.resolve(jsonResponse({ title: "Boom" }, 500));
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

// The GETs of the vocabulary this test made, by method and URL.
const listReads = (fetchMock: ReturnType<typeof stubFetch>) =>
  fetchMock.mock.calls.filter(([u, init]) => String(u) === "/api/v1/customers/groups" && !init?.method);

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
    expect(screen.getAllByText("No default")).toHaveLength(2);
    expect(screen.getAllByText("No customers")).toHaveLength(2);
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
        brandId: null,
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
        // Edited with its brand untouched, so the brand is sent back as it was.
        brandId: "b1",
      });
    });
  });

  it("shows each group's default brand, or the workspace default", async () => {
    stubFetch();
    renderModal();
    await screen.findByText("Retail");
    const row = (name: string) => within(screen.getByText(name).closest("tr") as HTMLElement);
    expect(row("Retail").getByText("Nordlys")).toBeInTheDocument();
    expect(row("Key accounts").getByText("Workspace default")).toBeInTheDocument();
    expect(row("Archived brand group").getByText("Old brand")).toBeInTheDocument();
  });

  it("offers the published brands and sends the chosen one with the group", async () => {
    const fetchMock = stubFetch();
    renderModal();
    await screen.findByText("Retail");
    await userEvent.type(screen.getByRole("textbox", { name: "Group name" }), "Energy");
    await userEvent.click(screen.getByRole("combobox", { name: "Default brand" }));
    await userEvent.click(await screen.findByRole("option", { name: "Fjordkraft Demo" }));
    await userEvent.click(screen.getByRole("button", { name: "Create group" }));

    await waitFor(() => {
      const posts = fetchMock.mock.calls.filter(([, init]) => (init as RequestInit)?.method === "POST");
      expect(posts).toHaveLength(1);
      expect(JSON.parse(String((posts[0][1] as RequestInit).body))).toEqual({
        name: "Energy",
        defaultPaymentTermsDays: null,
        brandId: "b2",
      });
    });
  });

  it("keeps an archived brand on the edit form, named as archived, and clears it on request", async () => {
    const fetchMock = stubFetch();
    renderModal();
    await userEvent.click(await screen.findByRole("button", { name: "Edit Archived brand group" }));
    expect(screen.getByRole("combobox", { name: "Default brand" })).toHaveValue("Old brand (archived)");
    await userEvent.click(screen.getByRole("combobox", { name: "Default brand" }));
    await userEvent.click(await screen.findByRole("option", { name: "Use the workspace default brand" }));
    await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

    await waitFor(() => {
      const puts = fetchMock.mock.calls.filter(([, init]) => (init as RequestInit)?.method === "PUT");
      expect(puts).toHaveLength(1);
      expect(JSON.parse(String((puts[0][1] as RequestInit).body))).toMatchObject({ brandId: null });
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
  it("reads the list again when a delete is refused, so the row stops offering it", async () => {
    // The row's count said 0 and its delete was enabled; the server says the
    // group is in use. Left as it was, the next click fails the same way.
    const fetchMock = stubFetch({ deleteInUse: true });
    renderModal();
    await userEvent.click(await screen.findByRole("button", { name: "Delete Key accounts" }));
    await userEvent.click(screen.getByRole("button", { name: "Delete group" }));

    await waitFor(() =>
      expect(fetchMock.mock.calls.filter(([, init]) => (init as RequestInit)?.method === "DELETE")).toHaveLength(1),
    );
    await waitFor(() => expect(listReads(fetchMock)).toHaveLength(2));
  });

  it("says nothing about an empty vocabulary while it is loading", async () => {
    stubFetch({ list: "hang" });
    renderModal();
    await screen.findByRole("dialog");
    expect(screen.queryByText("No groups yet.")).not.toBeInTheDocument();
  });

  it("says the list failed to load rather than that there are no groups", async () => {
    // "No groups yet" on a failed read would invite creating a name that exists.
    stubFetch({ list: "fail" });
    renderModal();
    expect(await screen.findByText("Could not load the groups.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Try again" })).toBeInTheDocument();
    expect(screen.queryByText("No groups yet.")).not.toBeInTheDocument();
  });

  it("opens on the list and the create form again after being closed mid-edit", async () => {
    // The modal's component outlives a close; an edit or a delete left open
    // must not greet the next opening, seeded from what may be a stale list.
    stubFetch();
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const Harness = () => {
      const [opened, setOpened] = useState(true);
      return (
        <MantineProvider env="test">
          <QueryClientProvider client={client}>
            <button type="button" onClick={() => setOpened(true)}>
              Reopen
            </button>
            <ManageGroupsModal opened={opened} onClose={() => setOpened(false)} />
          </QueryClientProvider>
        </MantineProvider>
      );
    };
    render(<Harness />);
    await userEvent.click(await screen.findByRole("button", { name: "Edit Retail" }));
    await userEvent.click(screen.getByRole("button", { name: "Delete Key accounts" }));
    expect(screen.getByRole("button", { name: "Save changes" })).toBeInTheDocument();

    await userEvent.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    await userEvent.click(screen.getByRole("button", { name: "Reopen" }));

    expect(await screen.findByRole("button", { name: "Create group" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Save changes" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Delete group" })).not.toBeInTheDocument();
  });
});
