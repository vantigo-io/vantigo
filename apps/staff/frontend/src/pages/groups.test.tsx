import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { entraProvider, meta } from "../test/fixtures";
import { choose, sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { stubStaffApi } from "../test/server";

const openList = async () => {
  // The slot holds Microsoft Entra ID: /meta names it, so a badge says its name.
  const fetchMock = stubStaffApi({ meta: meta("manage", { providers: [entraProvider()] }) });
  const view = renderRoute("/staff/groups");
  const table = await screen.findByRole("table", { name: "Groups" });
  return { fetchMock, table, ...view };
};

const openNewGroup = async () => {
  const opened = await openList();
  await userEvent.click(screen.getByRole("button", { name: "New group" }));
  const dialog = await screen.findByRole("dialog", { name: "New group" });
  return { ...opened, dialog };
};

describe("Groups — the list", () => {
  it("lists the groups in use with their key, kind, members and each provider's state", async () => {
    const { table } = await openList();
    const engineering = within(table).getByRole("row", { name: /^Engineering engineering/ });
    expect(within(engineering).getByText("Listed")).toBeInTheDocument();
    expect(within(engineering).getByText("2")).toBeInTheDocument();
    expect(within(engineering).getByText("Microsoft Entra ID: In sync")).toBeInTheDocument();
    const derived = within(table).getByRole("row", { name: /Engineering department/ });
    expect(within(derived).getByText("Derived")).toBeInTheDocument();
    expect(within(table).queryByText("Old project")).not.toBeInTheDocument();
  });

  it("shows the archived behind the switch", async () => {
    const { table } = await openList();
    await userEvent.click(screen.getByRole("switch", { name: "Show archived" }));
    const old = within(table).getByRole("row", { name: /Old project/ });
    expect(within(old).getByText("Archived")).toBeInTheDocument();
    expect(within(old).getByText("Microsoft Entra ID: Kept, renamed")).toBeInTheDocument();
  });

  it("is refused to anyone but HR", async () => {
    stubStaffApi({ role: "view" });
    renderRoute("/staff/groups");
    expect(await screen.findByText("The groups could not be loaded")).toBeInTheDocument();
  });
});

describe("Groups — New group and the rule's fields per kind", () => {
  it("asks for no rule for a listed group, and opens the new group's page once it is saved", async () => {
    const { fetchMock, dialog, router } = await openNewGroup();
    expect(within(dialog).queryByRole("combobox", { name: "Rule" })).not.toBeInTheDocument();
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Key" }), "sales");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Name" }), "Sales");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(sent(fetchMock, "POST", /\/groups$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/groups$/)[0].body).toEqual({ key: "sales", name: "Sales", kind: "listed" });
    await waitFor(() => expect(router.state.location.pathname).toBe("/staff/groups/900"));
    expect(await screen.findByRole("heading", { name: "Sales" })).toBeInTheDocument();
  });

  it("refuses a key the pattern does not admit before anything is sent", async () => {
    const { fetchMock, dialog } = await openNewGroup();
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Key" }), "Sales Team");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Name" }), "Sales");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    expect(await within(dialog).findByText(/1 to 60 lower-case letters/)).toBeInTheDocument();
    expect(sent(fetchMock, "POST", /\/groups$/)).toHaveLength(0);
  });

  it("puts the server's field error beside the key", async () => {
    const { dialog } = await openNewGroup();
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Key" }), "engineering");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Name" }), "Again");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    expect(await within(dialog).findByText("Another group has this key")).toBeInTheDocument();
  });

  it("offers a department, and the departments beneath it, for a department rule", async () => {
    const { fetchMock, dialog } = await openNewGroup();
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Key" }), "eng-all");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Name" }), "All of engineering");
    await userEvent.click(within(dialog).getByRole("radio", { name: "Derived" }));
    await choose(dialog, "Rule", "Department");
    await choose(dialog, "Department", "Engineering");
    await userEvent.click(within(dialog).getByRole("switch", { name: /Include departments beneath/ }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(sent(fetchMock, "POST", /\/groups$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/groups$/)[0].body).toEqual({
      key: "eng-all",
      name: "All of engineering",
      kind: "derived",
      rule: { kind: "department", id: 21, includeBelow: true },
    });
  });

  it("offers a unit, a location and an employee type by name, and nothing for everyone employed", async () => {
    const { dialog } = await openNewGroup();
    await userEvent.click(within(dialog).getByRole("radio", { name: "Derived" }));
    await choose(dialog, "Rule", "Business unit");
    await userEvent.click(within(dialog).getByRole("combobox", { name: "Business unit" }));
    expect(await screen.findByRole("option", { name: "Oslo office" })).toBeInTheDocument();
    expect(within(dialog).queryByRole("switch", { name: "Include departments beneath" })).not.toBeInTheDocument();

    await choose(dialog, "Rule", "Location");
    await userEvent.click(within(dialog).getByRole("combobox", { name: "Location" }));
    expect(await screen.findByRole("option", { name: "Bergen" })).toBeInTheDocument();

    await choose(dialog, "Rule", "Employee type");
    await userEvent.click(within(dialog).getByRole("combobox", { name: "Employee type" }));
    expect(await screen.findByRole("option", { name: "Contractor" })).toBeInTheDocument();

    await choose(dialog, "Rule", "Everyone employed");
    expect(within(dialog).getByText("Everyone with an employment in force today.")).toBeInTheDocument();
  });

  it("picks the manager from the directory for a reports-to rule", async () => {
    const { fetchMock, dialog } = await openNewGroup();
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Key" }), "ada-team");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Name" }), "Ada's team");
    await userEvent.click(within(dialog).getByRole("radio", { name: "Derived" }));
    await choose(dialog, "Rule", "Reports to");
    await userEvent.type(within(dialog).getByRole("combobox", { name: "Manager" }), "ada");
    await userEvent.click(await screen.findByRole("option", { name: "Ada Lovelace (E-1)" }, { timeout: 10_000 }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(sent(fetchMock, "POST", /\/groups$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/groups$/)[0].body.rule).toEqual({ kind: "reportsTo", id: 101 });
  });

  it("asks for the rule's department before sending a department rule", async () => {
    const { fetchMock, dialog } = await openNewGroup();
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Key" }), "eng");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Name" }), "Eng");
    await userEvent.click(within(dialog).getByRole("radio", { name: "Derived" }));
    await choose(dialog, "Rule", "Department");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    expect(await within(dialog).findByText("Choose one")).toBeInTheDocument();
    expect(sent(fetchMock, "POST", /\/groups$/)).toHaveLength(0);
  });
});
