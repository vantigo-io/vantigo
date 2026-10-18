import { QueryClient } from "@tanstack/react-query";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
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
  listInvitations: vi.fn(),
}));

vi.mock("../../api/users", () => ({
  listUsers: api.listUsers,
  manageUser: api.manageUser,
  unmanageUser: api.unmanageUser,
  enableUser: api.enableUser,
  disableUser: api.disableUser,
  deleteUser: api.deleteUser,
  createUser: vi.fn(),
  updateUser: vi.fn(),
  sendPasswordReset: vi.fn(),
  setUserPassword: vi.fn(),
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
    fireEvent.change(await screen.findByLabelText("Employee number"), { target: { value: " 1042 " } });
    fireEvent.click(screen.getByRole("button", { name: "Adopt" }));
    await waitFor(() => expect(api.manageUser).toHaveBeenCalledWith("u-1", "1042"));
    expect(await screen.findByText("Staff now manages the account")).toBeInTheDocument();
  });

  it("refuses a number outside Staff's alphabet before asking the server", async () => {
    await renderUsers([user({})]);
    openActions("Kari Nordmann");
    fireEvent.click(await screen.findByRole("menuitem", { name: "Adopt for Staff" }));
    fireEvent.change(await screen.findByLabelText("Employee number"), { target: { value: "10 42" } });
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
    fireEvent.change(await screen.findByLabelText("Employee number"), { target: { value: "1042" } });
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
  it("hides Delete for a user Staff manages, and says why", async () => {
    await renderUsers([user({ managedBy: "staff", employeeNumber: "1042" })]);
    openActions("Kari Nordmann");
    await screen.findByRole("menuitem", { name: "Stop managing" });
    expect(screen.queryByRole("menuitem", { name: "Delete user" })).not.toBeInTheDocument();
    expect(
      screen.getByText("Staff manages this account, so it cannot be deleted. Stop managing it first."),
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
