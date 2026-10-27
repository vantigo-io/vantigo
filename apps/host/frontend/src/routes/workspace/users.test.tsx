import { QueryClient } from "@tanstack/react-query";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ManagedUser } from "../../api/users";
import { renderRoute } from "../../test/render-route";
import { Route as UsersRoute } from "./users";
import "../../i18n";

const api = vi.hoisted(() => ({
  listUsers: vi.fn(),
  manageUser: vi.fn(),
  unmanageUser: vi.fn(),
  enableUser: vi.fn(),
  disableUser: vi.fn(),
  deleteUser: vi.fn(),
  updateUser: vi.fn(),
  sendPasswordReset: vi.fn(),
  listInvitations: vi.fn(),
  announceSsoLink: vi.fn(),
  removeSsoLink: vi.fn(),
}));

vi.mock("../../api/users", () => ({
  listUsers: api.listUsers,
  manageUser: api.manageUser,
  unmanageUser: api.unmanageUser,
  enableUser: api.enableUser,
  disableUser: api.disableUser,
  deleteUser: api.deleteUser,
  createUser: vi.fn(),
  updateUser: api.updateUser,
  sendPasswordReset: api.sendPasswordReset,
  setUserPassword: vi.fn(),
  announceSsoLink: api.announceSsoLink,
  removeSsoLink: api.removeSsoLink,
}));

vi.mock("../../api/account-lifecycle", () => ({
  listInvitations: api.listInvitations,
  createInvitation: vi.fn(),
}));

const user = (overrides: Partial<ManagedUser>): ManagedUser => ({
  id: "u-1",
  displayName: "Kari Nordmann",
  email: "kari@example.com",
  role: "User",
  active: true,
  lockoutEnd: null,
  lockedOut: false,
  disabled: false,
  twoFactorEnabled: false,
  avatarUrl: null,
  ssoEnabled: false,
  pending: false,
  ...overrides,
});

/** An identity refusal as the API client throws it: the server's English message, the code beside it. */
const refusal = (status: number, code: string) => Object.assign(new Error(`server text for ${code}`), { status, code });

const renderUsers = async (users: ManagedUser[]) => {
  api.listUsers.mockResolvedValue(users);
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  queryClient.setQueryData(["auth", "session"], { user: { id: "me", roles: ["Owner"] } });
  await renderRoute("/workspace/users", UsersRoute.options.component as never, queryClient);
  await screen.findAllByText(users[0]?.displayName ?? "");
};

/** The desktop table's row for a user: the page also renders the mobile cards, hidden by CSS. */
const row = (name: string) => screen.getAllByText(name)[0].closest("tr") as HTMLElement;

/** The edit dialog's Role select: its visible input, not the hidden value field beside it. */
const roleInput = async () =>
  (await within(await screen.findByRole("dialog")).findAllByLabelText("Role")).find(
    (element) => element.tagName === "INPUT" && (element as HTMLInputElement).type !== "hidden",
  ) as HTMLElement;

const openActions = (name: string) => {
  fireEvent.click(within(row(name)).getByRole("button", { name: `Actions for ${name}` }));
};

beforeEach(() => {
  for (const fn of Object.values(api)) fn.mockReset();
  api.listInvitations.mockResolvedValue([]);
});

describe("the users page's statuses", () => {
  it("names a pending user Pending, not disabled", async () => {
    await renderUsers([
      user({ pending: true, disabled: true, active: false, managedBy: "staff", employeeNumber: "1042" }),
    ]);
    expect(within(row("Kari Nordmann")).getByText("Pending")).toBeInTheDocument();
    expect(within(row("Kari Nordmann")).queryByText(/disabled/i)).not.toBeInTheDocument();
  });

  it("says a pending user an Owner disabled is both", async () => {
    await renderUsers([
      user({
        pending: true,
        disabled: true,
        active: false,
        disabledBy: "owner",
        managedBy: "staff",
        employeeNumber: "1",
      }),
    ]);
    expect(within(row("Kari Nordmann")).getByText("Pending · Disabled by an Owner")).toBeInTheDocument();
  });

  it("says who disabled a user: an Owner or Staff", async () => {
    await renderUsers([
      user({ id: "u-1", displayName: "Kari Nordmann", disabled: true, active: false, disabledBy: "owner" }),
      user({
        id: "u-2",
        displayName: "Ola Hansen",
        disabled: true,
        active: false,
        disabledBy: "staff",
        managedBy: "staff",
        employeeNumber: "7",
      }),
    ]);
    expect(within(row("Kari Nordmann")).getByText("Disabled by an Owner")).toBeInTheDocument();
    expect(within(row("Ola Hansen")).getByText("Disabled by Staff")).toBeInTheDocument();
  });

  it("marks a user Staff manages, with the employee number", async () => {
    await renderUsers([user({ managedBy: "staff", employeeNumber: "A-1042" })]);
    expect(within(row("Kari Nordmann")).getByText("Managed by Staff · no. A-1042")).toBeInTheDocument();
  });
});

describe("adopting a user for Staff and stopping it", () => {
  it("asks the employee number and adopts the user with it", async () => {
    api.manageUser.mockResolvedValue(user({ managedBy: "staff", employeeNumber: "1042" }));
    await renderUsers([user({})]);
    openActions("Kari Nordmann");
    fireEvent.click(await screen.findByRole("menuitem", { name: "Adopt for Staff" }));
    fireEvent.change(await screen.findByLabelText(/Employee number/), { target: { value: " 1042 " } });
    fireEvent.click(screen.getByRole("button", { name: "Adopt" }));
    await waitFor(() => expect(api.manageUser).toHaveBeenCalledWith("u-1", "1042"));
    expect(await screen.findByText("Staff now manages the account")).toBeInTheDocument();
  });

  it("refuses a number outside Staff's alphabet before asking the server", async () => {
    await renderUsers([user({})]);
    openActions("Kari Nordmann");
    fireEvent.click(await screen.findByRole("menuitem", { name: "Adopt for Staff" }));
    const input = await screen.findByLabelText(/Employee number/);
    expect(input).toHaveAttribute("maxlength", "16");
    expect(input.closest(".mantine-InputWrapper-root")?.querySelector(".mantine-InputWrapper-required")).not.toBeNull();
    fireEvent.change(input, { target: { value: "10 42" } });
    fireEvent.click(screen.getByRole("button", { name: "Adopt" }));
    expect(
      await screen.findByText("Enter 1 to 16 characters, each a letter a–z, a digit or a hyphen."),
    ).toBeInTheDocument();
    expect(api.manageUser).not.toHaveBeenCalled();
  });

  it.each([
    ["employee_number_taken", "Another user already has this employee number."],
    [
      "account_privileged",
      "An Owner or a system administrator cannot be managed by Staff; an Owner looks after those accounts by hand.",
    ],
  ])("puts the refusal %s in words", async (code, words) => {
    api.manageUser.mockRejectedValue(refusal(409, code));
    await renderUsers([user({})]);
    openActions("Kari Nordmann");
    fireEvent.click(await screen.findByRole("menuitem", { name: "Adopt for Staff" }));
    fireEvent.change(await screen.findByLabelText(/Employee number/), { target: { value: "1042" } });
    fireEvent.click(screen.getByRole("button", { name: "Adopt" }));
    expect(await screen.findByText(words)).toBeInTheDocument();
    expect(screen.queryByText(`server text for ${code}`)).not.toBeInTheDocument();
  });

  it("does not offer adoption for an Owner, nor for a user Staff already manages", async () => {
    await renderUsers([
      user({ id: "u-1", displayName: "Kari Nordmann", role: "Owner" }),
      user({ id: "u-2", displayName: "Ola Hansen", managedBy: "staff", employeeNumber: "7" }),
    ]);
    openActions("Kari Nordmann");
    await screen.findByRole("menuitem", { name: "Edit details" });
    expect(screen.queryByRole("menuitem", { name: "Adopt for Staff" })).not.toBeInTheDocument();
    openActions("Kari Nordmann");
    await waitFor(() => expect(screen.queryByRole("menuitem", { name: "Edit details" })).not.toBeInTheDocument());
    openActions("Ola Hansen");
    await screen.findByRole("menuitem", { name: "Stop managing" });
    expect(screen.queryByRole("menuitem", { name: "Adopt for Staff" })).not.toBeInTheDocument();
  });

  it("stops Staff managing a user after a confirmation", async () => {
    api.unmanageUser.mockResolvedValue(user({}));
    await renderUsers([user({ managedBy: "staff", employeeNumber: "1042" })]);
    openActions("Kari Nordmann");
    fireEvent.click(await screen.findByRole("menuitem", { name: "Stop managing" }));
    expect(await screen.findByText("Stop Staff managing this account?")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Stop managing" }));
    await waitFor(() => expect(api.unmanageUser).toHaveBeenCalledWith("u-1"));
    expect(await screen.findByText("Staff no longer manages the account")).toBeInTheDocument();
  });
});

describe("what a Staff-managed or pending user cannot have done", () => {
  it("greys out Delete for a user Staff manages, and says why", async () => {
    await renderUsers([user({ managedBy: "staff", employeeNumber: "1042" })]);
    openActions("Kari Nordmann");
    const del = await screen.findByRole("menuitem", { name: /Delete user/ });
    expect(del).toBeDisabled();
    expect(
      within(del).getByText("Staff manages this account, so it cannot be deleted. Stop managing it first."),
    ).toBeInTheDocument();
  });

  it("still offers Disable access for a pending user Staff disabled, beside the greyed Enable", async () => {
    api.disableUser.mockResolvedValue(user({}));
    await renderUsers([
      user({
        pending: true,
        disabled: true,
        active: false,
        disabledBy: "staff",
        managedBy: "staff",
        employeeNumber: "1",
      }),
    ]);
    openActions("Kari Nordmann");
    expect(await screen.findByRole("menuitem", { name: /Enable access/ })).toBeDisabled();
    const disable = screen.getByRole("menuitem", { name: "Disable access" });
    expect(disable).toBeEnabled();
    fireEvent.click(disable);
    fireEvent.click(await screen.findByRole("button", { name: "Disable access" }));
    await waitFor(() => expect(api.disableUser).toHaveBeenCalledWith("u-1"));
  });

  it("offers no Disable access for a pending user an Owner already disabled", async () => {
    await renderUsers([
      user({
        pending: true,
        disabled: true,
        active: false,
        disabledBy: "owner",
        managedBy: "staff",
        employeeNumber: "1",
      }),
    ]);
    openActions("Kari Nordmann");
    await screen.findByRole("menuitem", { name: /Enable access/ });
    expect(screen.queryByRole("menuitem", { name: "Disable access" })).not.toBeInTheDocument();
  });

  it("greys out the password reset and the initial password for a pending user, and says why", async () => {
    await renderUsers([
      user({ pending: true, disabled: true, active: false, managedBy: "staff", employeeNumber: "1" }),
    ]);
    openActions("Kari Nordmann");
    for (const name of [/Send reset email/, /Set initial password/]) {
      const item = await screen.findByRole("menuitem", { name });
      expect(item).toBeDisabled();
      expect(
        within(item).getByText("Not accepted their invitation yet: the account opens only when they accept it."),
      ).toBeInTheDocument();
    }
  });

  it("keeps the password reset for a user who is not pending", async () => {
    await renderUsers([user({})]);
    openActions("Kari Nordmann");
    expect(await screen.findByRole("menuitem", { name: "Send reset email" })).toBeEnabled();
    expect(screen.getByRole("menuitem", { name: "Set initial password" })).toBeEnabled();
  });

  const note = "Making them an Owner stops Staff managing this account.";
  const chooseOwner = async (name: string) => {
    openActions(name);
    fireEvent.click(await screen.findByRole("menuitem", { name: "Edit details" }));
    const role = await roleInput();
    expect(screen.queryByText(note)).not.toBeInTheDocument();
    fireEvent.click(role);
    fireEvent.click(await screen.findByRole("option", { name: "Owner" }));
    await waitFor(() => expect(role).toHaveValue("Owner"));
  };

  it("notes in the edit dialog that making a managed user an Owner stops Staff managing them", async () => {
    await renderUsers([user({ managedBy: "staff", employeeNumber: "1" })]);
    await chooseOwner("Kari Nordmann");
    expect(screen.getByText(note)).toBeInTheDocument();
  });

  it("makes no such note for a user Staff does not manage", async () => {
    await renderUsers([user({})]);
    await chooseOwner("Kari Nordmann");
    expect(screen.queryByText(note)).not.toBeInTheDocument();
  });

  it("words a refused promotion of a pending user as such", async () => {
    api.updateUser.mockRejectedValue(refusal(409, "account_pending"));
    await renderUsers([
      user({ pending: true, disabled: true, active: false, managedBy: "staff", employeeNumber: "1" }),
    ]);
    openActions("Kari Nordmann");
    fireEvent.click(await screen.findByRole("menuitem", { name: "Edit details" }));
    fireEvent.click(await roleInput());
    fireEvent.click(await screen.findByRole("option", { name: "Owner" }));
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(
      await screen.findByText("A pending user cannot be made an Owner until they have accepted their invitation."),
    ).toBeInTheDocument();
  });

  it("still offers Delete for a user Staff does not manage", async () => {
    await renderUsers([user({})]);
    openActions("Kari Nordmann");
    expect(await screen.findByRole("menuitem", { name: "Delete user" })).toBeInTheDocument();
  });

  it("disables Enable access for a pending user, and says why", async () => {
    await renderUsers([
      user({ pending: true, disabled: true, active: false, managedBy: "staff", employeeNumber: "1042" }),
    ]);
    openActions("Kari Nordmann");
    const enable = await screen.findByRole("menuitem", { name: /Enable access/ });
    expect(enable).toBeDisabled();
    expect(
      within(enable).getByText("Not accepted their invitation yet: the account opens only when they accept it."),
    ).toBeInTheDocument();
  });

  it("keeps Enable access for a user an Owner disabled", async () => {
    await renderUsers([user({ disabled: true, active: false, disabledBy: "owner" })]);
    openActions("Kari Nordmann");
    expect(await screen.findByRole("menuitem", { name: "Enable access" })).toBeEnabled();
  });

  it("puts a delete refused account_managed in words", async () => {
    api.deleteUser.mockRejectedValue(refusal(409, "account_managed"));
    await renderUsers([user({})]);
    openActions("Kari Nordmann");
    fireEvent.click(await screen.findByRole("menuitem", { name: "Delete user" }));
    fireEvent.click(await screen.findByRole("button", { name: "Delete user" }));
    expect(
      await screen.findByText("Staff manages this account, so it cannot be deleted. Stop managing it first."),
    ).toBeInTheDocument();
  });
});

describe("the single sign-on column", () => {
  it("is absent when no user carries an ssoLink", async () => {
    await renderUsers([user({})]);
    expect(screen.queryByText("Single sign-on")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Connect…" })).not.toBeInTheDocument();
  });

  it("says each of the three states in words", async () => {
    await renderUsers([
      user({
        id: "u-1",
        displayName: "Kari Nordmann",
        ssoLink: { state: "linked", userPrincipalName: "kari@example.com" },
      }),
      user({
        id: "u-2",
        displayName: "Ola Hansen",
        ssoLink: { state: "expected", userPrincipalName: "ola@example.com", expectedAt: "2026-10-24T10:00:00Z" },
      }),
      user({ id: "u-3", displayName: "Per Olsen", ssoLink: { state: "none" } }),
    ]);
    expect(screen.getAllByText("Single sign-on").length).toBeGreaterThan(0);
    expect(within(row("Kari Nordmann")).getByText("Connected")).toBeInTheDocument();
    expect(within(row("Ola Hansen")).getByText("Awaiting first sign-in")).toBeInTheDocument();
    expect(within(row("Ola Hansen")).getByText("ola@example.com")).toBeInTheDocument();
    expect(within(row("Per Olsen")).getByRole("button", { name: "Connect…" })).toBeInTheDocument();
  });

  it("announces the link with the pre-filled address", async () => {
    api.announceSsoLink.mockResolvedValue(
      user({ ssoLink: { state: "expected", userPrincipalName: "kari@example.com" } }),
    );
    await renderUsers([user({ ssoLink: { state: "none" } })]);
    fireEvent.click(within(row("Kari Nordmann")).getByRole("button", { name: "Connect…" }));
    expect(await screen.findByText("Connect Kari Nordmann to single sign-on")).toBeInTheDocument();
    expect(screen.getByLabelText("User principal name")).toHaveValue("kari@example.com");
    fireEvent.click(screen.getByRole("button", { name: "Connect" }));
    await waitFor(() =>
      expect(api.announceSsoLink).toHaveBeenCalledWith("u-1", { userPrincipalName: "kari@example.com" }),
    );
    expect(await screen.findByText("Connection announced")).toBeInTheDocument();
  });

  it("puts a sso_link_taken refusal in words", async () => {
    api.announceSsoLink.mockRejectedValue(refusal(409, "sso_link_taken"));
    await renderUsers([user({ ssoLink: { state: "none" } })]);
    fireEvent.click(within(row("Kari Nordmann")).getByRole("button", { name: "Connect…" }));
    fireEvent.click(await screen.findByRole("button", { name: "Connect" }));
    expect(await screen.findByText("Another user is already connected to this account.")).toBeInTheDocument();
    expect(screen.queryByText("server text for sso_link_taken")).not.toBeInTheDocument();
  });

  it("disconnects a linked user after a confirmation", async () => {
    api.removeSsoLink.mockResolvedValue(user({ ssoLink: { state: "none" } }));
    await renderUsers([user({ ssoLink: { state: "linked" } })]);
    openActions("Kari Nordmann");
    fireEvent.click(await screen.findByRole("menuitem", { name: "Disconnect from single sign-on…" }));
    expect(await screen.findByText("Disconnect from single sign-on?")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Disconnect" }));
    await waitFor(() => expect(api.removeSsoLink).toHaveBeenCalledWith("u-1"));
  });

  it("offers a pending user no Connect, button or menu item", async () => {
    await renderUsers([
      user({
        pending: true,
        disabled: true,
        active: false,
        managedBy: "staff",
        employeeNumber: "1",
        ssoLink: { state: "none" },
      }),
    ]);
    expect(screen.queryByRole("button", { name: "Connect…" })).not.toBeInTheDocument();
    openActions("Kari Nordmann");
    await screen.findByRole("menuitem", { name: "Edit details" });
    expect(screen.queryByRole("menuitem", { name: "Connect to single sign-on…" })).not.toBeInTheDocument();
  });
});

describe("the Entra directory in the users screen", () => {
  const account = {
    objectId: "44444444-4444-4444-8444-444444444444",
    displayName: "Kari Nordmann",
    mail: "kari.nordmann@contoso.no",
    userPrincipalName: "kari.nordmann@contoso.no",
    employeeId: "1042",
    accountEnabled: true,
  };
  const olaAccount = {
    objectId: "55555555-5555-4555-8555-555555555555",
    displayName: "Ola Hansen",
    userPrincipalName: "ola.hansen@contoso.no",
    accountEnabled: true,
  };
  const json = (body: unknown, status = 200) =>
    new Response(JSON.stringify(body), {
      status,
      headers: { "Content-Type": status === 200 ? "application/json" : "application/problem+json" },
    });

  /** The Entra module's two reads, faked at the network as its package calls them. */
  const stubDirectory = () => {
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.startsWith("/api/v1/entra/directory/users?")) return Promise.resolve(json({ users: [account] }));
      if (url === "/api/v1/entra/directory/matches")
        return Promise.resolve(
          json({
            matches: [
              { userId: "u-1", account, matchedBy: ["userPrincipalName", "employeeId"] },
              { userId: "u-2", account: olaAccount, matchedBy: ["userPrincipalName"] },
            ],
            candidates: 2,
            ambiguous: 0,
            truncated: false,
          }),
        );
      return Promise.resolve(new Response(null, { status: 404 }));
    });
    vi.stubGlobal("fetch", fetchMock);
    return fetchMock;
  };

  afterEach(() => {
    delete window.__VANTIGO_APP__;
  });

  it("fills both keys from the account picked in the directory", async () => {
    stubDirectory();
    api.announceSsoLink.mockResolvedValue(user({ ssoLink: { state: "expected" } }));
    await renderUsers([user({ ssoLink: { state: "none" } })]);
    fireEvent.click(within(row("Kari Nordmann")).getByRole("button", { name: "Connect…" }));
    await userEvent.type(await screen.findByLabelText(/Search the directory/), "kari");
    await userEvent.click(await screen.findByRole("option", { name: /kari\.nordmann@contoso\.no/ }));
    expect(screen.getByLabelText("User principal name")).toHaveValue("kari.nordmann@contoso.no");
    expect(screen.getByLabelText("Object id")).toHaveValue(account.objectId);
    fireEvent.click(screen.getByRole("button", { name: "Connect" }));
    await waitFor(() =>
      expect(api.announceSsoLink).toHaveBeenCalledWith("u-1", {
        userPrincipalName: "kari.nordmann@contoso.no",
        objectId: account.objectId,
      }),
    );
  });

  it("forgets the picked object id when the principal name is edited away from the pick", async () => {
    stubDirectory();
    api.announceSsoLink.mockResolvedValue(user({ ssoLink: { state: "expected" } }));
    await renderUsers([user({ ssoLink: { state: "none" } })]);
    fireEvent.click(within(row("Kari Nordmann")).getByRole("button", { name: "Connect…" }));
    await userEvent.type(await screen.findByLabelText(/Search the directory/), "kari");
    await userEvent.click(await screen.findByRole("option", { name: /kari\.nordmann@contoso\.no/ }));
    expect(screen.getByLabelText("Object id")).toHaveValue(account.objectId);
    const upn = screen.getByLabelText("User principal name");
    await userEvent.clear(upn);
    await userEvent.type(upn, "someone.else@contoso.no");
    expect(screen.getByLabelText("Object id")).toHaveValue("");
    fireEvent.click(screen.getByRole("button", { name: "Connect" }));
    await waitFor(() =>
      expect(api.announceSsoLink).toHaveBeenCalledWith("u-1", { userPrincipalName: "someone.else@contoso.no" }),
    );
  });

  it("clears the picked pair when the directory search is changed after a pick", async () => {
    stubDirectory();
    await renderUsers([user({ ssoLink: { state: "none" } })]);
    fireEvent.click(within(row("Kari Nordmann")).getByRole("button", { name: "Connect…" }));
    const search = await screen.findByLabelText(/Search the directory/);
    await userEvent.type(search, "kari");
    await userEvent.click(await screen.findByRole("option", { name: /kari\.nordmann@contoso\.no/ }));
    expect(screen.getByLabelText("User principal name")).toHaveValue("kari.nordmann@contoso.no");
    await userEvent.type(search, "x");
    expect(screen.getByLabelText("User principal name")).toHaveValue("");
    expect(screen.getByLabelText("Object id")).toHaveValue("");
  });

  it("leaves an Owner's match unchecked in the review, to be checked", async () => {
    stubDirectory();
    await renderUsers([
      user({ id: "u-1", displayName: "Kari Nordmann", ssoLink: { state: "none" } }),
      user({
        id: "u-2",
        displayName: "Ola Hansen",
        email: "ola@example.com",
        role: "Owner",
        ssoLink: { state: "none" },
      }),
    ]);
    fireEvent.click(screen.getByRole("button", { name: "Find matches in the directory…" }));
    const dialog = await screen.findByRole("dialog", { name: "Users who match an account in the directory" });
    expect(await within(dialog).findByRole("button", { name: "Connect selected (1)" })).toBeEnabled();
    expect(
      within(dialog).getByRole("checkbox", { name: "Connect Ola Hansen to ola.hansen@contoso.no" }),
    ).not.toBeChecked();
    expect(within(dialog).getByText("Check this match")).toBeInTheDocument();
  });

  it("keeps the typed field alone while the module is off", async () => {
    window.__VANTIGO_APP__ = { basePath: "/", title: "Vantigo", support: {}, modules: ["staff"] };
    await renderUsers([user({ ssoLink: { state: "none" } })]);
    expect(screen.queryByRole("button", { name: "Find matches in the directory…" })).not.toBeInTheDocument();
    fireEvent.click(within(row("Kari Nordmann")).getByRole("button", { name: "Connect…" }));
    expect(await screen.findByLabelText("User principal name")).toHaveValue("kari@example.com");
    expect(screen.queryByLabelText(/Search the directory/)).not.toBeInTheDocument();
  });

  it("connects the selected pairs from the matches, in the Owner's words when refused", async () => {
    stubDirectory();
    api.announceSsoLink.mockImplementation((id: string) =>
      id === "u-2" ? Promise.reject(refusal(409, "sso_link_taken")) : Promise.resolve(user({})),
    );
    await renderUsers([
      user({ id: "u-1", displayName: "Kari Nordmann", ssoLink: { state: "none" } }),
      user({ id: "u-2", displayName: "Ola Hansen", email: "ola@example.com", ssoLink: { state: "none" } }),
    ]);
    fireEvent.click(screen.getByRole("button", { name: "Find matches in the directory…" }));
    const dialog = await screen.findByRole("dialog", { name: "Users who match an account in the directory" });
    await userEvent.click(await within(dialog).findByRole("button", { name: "Connect selected (2)" }));
    await waitFor(() => expect(api.announceSsoLink).toHaveBeenCalledTimes(2));
    expect(api.announceSsoLink).toHaveBeenCalledWith("u-1", {
      objectId: account.objectId,
      userPrincipalName: "kari.nordmann@contoso.no",
    });
    expect(api.announceSsoLink).toHaveBeenCalledWith("u-2", {
      objectId: olaAccount.objectId,
      userPrincipalName: "ola.hansen@contoso.no",
    });
    expect(await within(dialog).findByText("Another user is already connected to this account.")).toBeInTheDocument();
    expect(within(dialog).getByText("Connected")).toBeInTheDocument();
    // The screen's users are read again once the review is through.
    await waitFor(() => expect(api.listUsers).toHaveBeenCalledTimes(2));
  });
});
