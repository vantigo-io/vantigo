import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { setLanguagePreference } from "@vantigo/frontend-shell";
import { describe, expect, it } from "vitest";
import { settings } from "../test/fixtures";
import { entraSection } from "../test/group-fixtures";
import { region, sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { type StaffServer, staffProblem, stubStaffApi } from "../test/server";

const openGroup = async (id: number, server: StaffServer = {}) => {
  const fetchMock = stubStaffApi(server);
  const view = renderRoute(`/staff/groups/${id}`);
  const card = await region("Microsoft Entra ID");
  return { fetchMock, card, ...view };
};

describe("The group page — the header and its actions", () => {
  it("says the kind and the rule in words, and offers Delete only while no provider made the group", async () => {
    await openGroup(1002);
    expect(screen.getByRole("heading", { name: "Engineering department" })).toBeInTheDocument();
    expect(
      await screen.findByText(/Derived · engineering-dept · the department Engineering and those beneath it/),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Edit" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Delete" })).toBeInTheDocument();
  });

  it("offers no Delete for a group a provider made — it is archived instead, with a confirmation", async () => {
    const { fetchMock } = await openGroup(1001);
    expect(screen.queryByRole("button", { name: "Delete" })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Archive" }));
    const dialog = await screen.findByRole("dialog", { name: "Archive Engineering?" });
    expect(within(dialog).getByText(/renamed “… \(archived\)” and kept/)).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: "Archive" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/groups\/1001\/archive$/)).toHaveLength(1));
    expect(await screen.findByText(/The group is archived/)).toBeInTheDocument();
  });

  it("deletes a group never synced and goes back to the list", async () => {
    const { fetchMock, router } = await openGroup(1002);
    await userEvent.click(screen.getByRole("button", { name: "Delete" }));
    const dialog = await screen.findByRole("dialog", { name: "Delete Engineering department?" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Delete" }));
    await waitFor(() => expect(sent(fetchMock, "DELETE", /\/groups\/1002$/)).toHaveLength(1));
    await waitFor(() => expect(router.state.location.pathname).toBe("/staff/groups"));
  });

  it("edits the group without its key", async () => {
    const { fetchMock } = await openGroup(1001);
    await userEvent.click(screen.getByRole("button", { name: "Edit" }));
    const dialog = await screen.findByRole("dialog", { name: "Edit the group" });
    expect(within(dialog).queryByRole("textbox", { name: "Key" })).not.toBeInTheDocument();
    expect(within(dialog).getByText("Key: engineering — it never changes.")).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(sent(fetchMock, "PUT", /\/groups\/1001$/)).toHaveLength(1));
    const [put] = sent(fetchMock, "PUT", /\/groups\/1001$/);
    expect(put.ifMatch).toBe("3");
    expect(put.body).toEqual({ name: "Engineering", description: "Everyone who builds the product", kind: "listed" });
  });

  it("offers nothing to change on an archived group", async () => {
    const { card } = await openGroup(1003);
    expect(screen.queryByRole("button", { name: "Edit" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Archive" })).not.toBeInTheDocument();
    expect(within(card).getByText("Kept, renamed")).toBeInTheDocument();
    expect(within(card).getByRole("switch", { name: "In Microsoft Entra ID" })).toBeDisabled();
  });
});

describe("The group page — members and owners", () => {
  it("lists a derived group's members read-only, materialised by the rule", async () => {
    await openGroup(1002);
    const members = await region("Members");
    expect(within(members).getByText("Materialised daily by the rule.")).toBeInTheDocument();
    expect(within(members).getByText("Alan Turing")).toBeInTheDocument();
    expect(within(members).getByText("The rule")).toBeInTheDocument();
    expect(within(members).queryByRole("button", { name: "Save the members" })).not.toBeInTheDocument();
  });

  it("replaces a listed group's members with the people picked", async () => {
    const { fetchMock } = await openGroup(1001);
    const members = await region("Members");
    const picker = within(members).getByRole("combobox", { name: "The members" });
    await userEvent.type(picker, "alan");
    await userEvent.click(await screen.findByRole("option", { name: "Alan Turing (E-3)" }, { timeout: 10_000 }));
    await userEvent.click(within(members).getByRole("button", { name: "Save the members" }));
    await waitFor(() => expect(sent(fetchMock, "PUT", /\/groups\/1001\/members$/)).toHaveLength(1));
    expect(sent(fetchMock, "PUT", /\/groups\/1001\/members$/)[0].body).toEqual({ personIds: [101, 102, 103] });
  });

  it("replaces the owners with the people picked", async () => {
    const { fetchMock } = await openGroup(1001);
    const owners = await region("Owners");
    const picker = within(owners).getByRole("combobox", { name: "The owners" });
    await userEvent.type(picker, "grace");
    await userEvent.click(await screen.findByRole("option", { name: "Grace Hopper (E-2)" }, { timeout: 10_000 }));
    await userEvent.click(within(owners).getByRole("button", { name: "Save the owners" }));
    await waitFor(() => expect(sent(fetchMock, "PUT", /\/groups\/1001\/owners$/)).toHaveLength(1));
    expect(sent(fetchMock, "PUT", /\/groups\/1001\/owners$/)[0].body).toEqual({ personIds: [101, 102] });
  });
});

describe("The group page — a provider's traits, from its declaration", () => {
  it("renders every type: a select, a bounded text, a switch and a multi-select, the default applied to an empty form", async () => {
    const { card } = await openGroup(1002);
    await userEvent.click(within(card).getByRole("switch", { name: "In Microsoft Entra ID" }));

    // The required trait is marked; the default fills the empty form.
    const type = within(card).getByRole("combobox", { name: /Group type/ });
    expect(type).toHaveValue("Security");
    const marked = (input: HTMLElement) => (input as HTMLInputElement).labels?.[0]?.textContent?.includes("*");
    expect(marked(type)).toBe(true);
    expect(within(card).getByText("Fixed once the group is made.")).toBeInTheDocument();
    expect(within(card).getByRole("link", { name: "About this setting" })).toHaveAttribute(
      "href",
      "https://learn.microsoft.com/entra/fundamentals/concept-learn-about-groups",
    );
    const nickname = within(card).getByRole("textbox", { name: "Mail nickname" });
    expect(nickname).toHaveAttribute("maxlength", "64");
    expect(marked(nickname)).toBe(false);
    expect(within(card).getByRole("switch", { name: /Make it a team/ })).not.toBeChecked();
    await userEvent.click(within(card).getByRole("combobox", { name: "Licences" }));
    expect(await screen.findByRole("option", { name: "ENTERPRISEPACK (5 of 25 available)" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "EMS (2 of 10 available)" })).toBeInTheDocument();
  });

  it("says the labels in the reader's language with English as the fallback", async () => {
    setLanguagePreference("nb");
    try {
      const fetchMock = stubStaffApi({
        groupTraits: { 1001: [entraSection({ enabled: true, values: { type: "security" } })] },
      });
      renderRoute("/staff/groups/1001");
      const card = await region("Microsoft Entra ID");
      expect(fetchMock).toHaveBeenCalled();
      // The provider's Norwegian where it has one, its English where it has none.
      expect(within(card).getByRole("combobox", { name: /Gruppetype/ })).toHaveValue("Sikkerhet");
      expect(within(card).getByRole("combobox", { name: "Licences" })).toBeInTheDocument();
      expect(within(card).getByRole("switch", { name: "I Microsoft Entra ID" })).toBeChecked();
    } finally {
      setLanguagePreference("auto");
    }
  });

  it("sends the values by trait key when the provider is turned on and saved", async () => {
    const { fetchMock, card } = await openGroup(1002);
    await userEvent.click(within(card).getByRole("switch", { name: "In Microsoft Entra ID" }));
    await userEvent.type(within(card).getByRole("textbox", { name: "Mail nickname" }), "eng-dept");
    await userEvent.click(within(card).getByRole("switch", { name: /Make it a team/ }));
    await userEvent.click(within(card).getByRole("combobox", { name: "Licences" }));
    await userEvent.click(await screen.findByRole("option", { name: "EMS (2 of 10 available)" }));
    await userEvent.click(within(card).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(sent(fetchMock, "PUT", /\/groups\/1002\/traits\/entra$/)).toHaveLength(1));
    expect(sent(fetchMock, "PUT", /\/groups\/1002\/traits\/entra$/)[0].body).toEqual({
      enabled: true,
      values: { type: "security", mailNickname: "eng-dept", team: true, licences: ["EMS"] },
    });
    expect(await within(await region("Microsoft Entra ID")).findByText("Waiting")).toBeInTheDocument();
  });

  it("shows the server's field error beside its field", async () => {
    const { card } = await openGroup(1002, {
      write: (method, path) =>
        method === "PUT" && path.endsWith("/traits/entra")
          ? staffProblem(400, {
              title: "Invalid traits",
              errors: { mailNickname: ["Another group has this mail nickname"] },
            })
          : undefined,
    });
    await userEvent.click(within(card).getByRole("switch", { name: "In Microsoft Entra ID" }));
    await userEvent.click(within(card).getByRole("button", { name: "Save" }));
    const nickname = within(card).getByRole("textbox", { name: "Mail nickname" });
    await waitFor(() => expect(nickname).toHaveAttribute("aria-invalid", "true"));
    expect(within(card).getByText("Another group has this mail nickname")).toBeInTheDocument();
  });

  it("marks a required trait left empty before anything is sent", async () => {
    const { fetchMock, card } = await openGroup(1002, {
      groupTraits: {
        1002: [
          entraSection({
            traits: entraSection().traits.map((trait) =>
              trait.key === "type" ? { ...trait, default: undefined } : trait,
            ),
          }),
        ],
      },
    });
    await userEvent.click(within(card).getByRole("switch", { name: "In Microsoft Entra ID" }));
    await userEvent.click(within(card).getByRole("button", { name: "Save" }));
    expect(await within(card).findByText("Required")).toBeInTheDocument();
    expect(sent(fetchMock, "PUT", /\/traits\/entra$/)).toHaveLength(0);
  });

  it("says In sync since … and that Staff's group is in the provider too", async () => {
    const { card } = await openGroup(1001);
    expect(within(card).getByText(/^In sync since/)).toBeInTheDocument();
    expect(within(card).getByText("Staff's in Microsoft Entra ID too")).toBeInTheDocument();
    expect(within(card).getByRole("textbox", { name: "Mail nickname" })).toHaveValue("engineering");
  });

  it("shows the provider's refusal beside the field it names", async () => {
    const { card } = await openGroup(1001, {
      groupTraits: {
        1001: [
          entraSection({
            enabled: true,
            values: { type: "microsoft365", team: true },
            state: "failed",
            lastError: "trait_needs_owner",
            lastErrorField: "team",
          }),
        ],
      },
    });
    expect(within(card).getAllByText("Failed").length).toBeGreaterThan(0);
    // Beside the trait it names, inside the switch's own field, not as an alert above the form.
    const team = within(card).getByRole("switch", { name: /Make it a team/ });
    const field = team.closest(".mantine-Switch-root") as HTMLElement;
    expect(within(field).getByText("Failed: it needs a person as an owner")).toBeInTheDocument();
    expect(within(card).queryByRole("alert")).not.toBeInTheDocument();
  });

  it("says a provider that could not declare its traits is failing, with no form", async () => {
    const { card } = await openGroup(1002, {
      groupTraits: { 1002: [entraSection({ status: "failing", traits: [] })] },
    });
    expect(within(card).getByText(/could not say what a group can carry/)).toBeInTheDocument();
    expect(within(card).queryByRole("switch")).not.toBeInTheDocument();
  });
});

describe("The group page — turning a provider off", () => {
  it("says the group is deleted after the settings' days, and sends enabled false", async () => {
    const { fetchMock, card } = await openGroup(1001, { settings: { ...settings(), deleteExternalAfterDays: 30 } });
    await userEvent.click(within(card).getByRole("switch", { name: "In Microsoft Entra ID" }));
    const dialog = await screen.findByRole("dialog", { name: "Turn off Microsoft Entra ID for Engineering?" });
    expect(
      within(dialog).getByText("The group will be deleted in Microsoft Entra ID after 30 days."),
    ).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: "Turn off" }));
    await waitFor(() => expect(sent(fetchMock, "PUT", /\/groups\/1001\/traits\/entra$/)).toHaveLength(1));
    expect(sent(fetchMock, "PUT", /\/groups\/1001\/traits\/entra$/)[0].body).toEqual({ enabled: false });
  });

  it("says the group is kept and renamed when the setting is never", async () => {
    // An absent deleteExternalAfterDays is "never", as the server answers it.
    const never = settings();
    delete never.deleteExternalAfterDays;
    const { fetchMock, card } = await openGroup(1001, { settings: never });
    await userEvent.click(within(card).getByRole("switch", { name: "In Microsoft Entra ID" }));
    const dialog = await screen.findByRole("dialog", { name: "Turn off Microsoft Entra ID for Engineering?" });
    expect(
      within(dialog).getByText("The group will be kept in Microsoft Entra ID, renamed “Engineering (archived)”."),
    ).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
    expect(sent(fetchMock, "PUT", /\/traits\/entra$/)).toHaveLength(0);
    expect(within(card).getByRole("switch", { name: "In Microsoft Entra ID" })).toBeChecked();
  });

  it("sends nothing when a provider never on is switched on and off again", async () => {
    const { fetchMock, card } = await openGroup(1002);
    const toggle = within(card).getByRole("switch", { name: "In Microsoft Entra ID" });
    await userEvent.click(toggle);
    await userEvent.click(toggle);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(sent(fetchMock, "PUT", /\/traits\/entra$/)).toHaveLength(0);
  });
});
