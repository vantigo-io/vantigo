import { QueryClient } from "@tanstack/react-query";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { AuthorizationRole, PermissionDescriptor } from "../../api/authorization";
import { renderRoute } from "../../test/render-route";
import { Route as RolesRoute } from "./roles";
import "../../i18n";

const api = vi.hoisted(() => ({
  getAuthorizationMe: vi.fn(),
  listPermissionCatalog: vi.fn(),
  listAuthorizationRoles: vi.fn(),
  updateAuthorizationRole: vi.fn(),
  listProvisioningRoles: vi.fn(),
  replaceProvisioningRoles: vi.fn(),
}));

vi.mock("../../api/authorization", () => ({
  getAuthorizationMe: api.getAuthorizationMe,
  listPermissionCatalog: api.listPermissionCatalog,
  listAuthorizationRoles: api.listAuthorizationRoles,
  updateAuthorizationRole: api.updateAuthorizationRole,
  listAuthorizationUsers: () => Promise.resolve([]),
  listDelegations: () => Promise.resolve([]),
  assignUserRoles: vi.fn(),
  createAuthorizationRole: vi.fn(),
  createDelegation: vi.fn(),
  deleteAuthorizationRole: vi.fn(),
  getUserAccess: vi.fn(),
  revokeDelegation: vi.fn(),
}));

vi.mock("../../api/provisioning-roles", () => ({
  listProvisioningRoles: api.listProvisioningRoles,
  replaceProvisioningRoles: api.replaceProvisioningRoles,
}));

const permission = (key: string, displayName: string, delegable = true): PermissionDescriptor => ({
  key,
  module: key.split(":")[0],
  category: "General",
  displayName,
  description: displayName,
  sensitive: false,
  delegable,
});

const catalog = [
  permission("identity:manage", "Manage identity", false),
  permission("staff:access", "Use Staff"),
  permission("staff:manage", "Manage staff"),
  permission("time:access", "Use Time"),
  permission("energy:secret", "Secret energy", false),
];

const role = (id: string, displayName: string, permissions: string[], builtIn = false): AuthorizationRole => ({
  id,
  name: displayName.toLowerCase().replace(/\s/g, "-"),
  normalizedName: displayName.toUpperCase(),
  displayName,
  description: `${displayName} role`,
  isSystem: false,
  isBuiltIn: builtIn,
  stewardUserId: null,
  version: `v-${id}`,
  permissions,
});

const roles = [
  role("r-user", "User", [], true),
  role("r-employee", "Employee", ["staff:access", "time:access"]),
  role("r-timer", "Timekeeper", ["time:access"]),
  role("r-hr", "HR lead", ["staff:access", "staff:manage"]),
  role("r-meter", "Meter reader", ["energy:secret"]),
];

const refusal = (status: number, code: string) => Object.assign(new Error(`server text for ${code}`), { status, code });

const renderRoles = async () => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await renderRoute("/workspace/roles", RolesRoute.options.component as never, queryClient);
  return screen.findByRole("heading", { name: "Roles Staff may give" });
};

/** The section's card, which names every custom role once more, beside its own permissions. */
const section = async () => (await renderRoles()).closest(".mantine-Card-root") as HTMLElement;

beforeEach(() => {
  for (const fn of Object.values(api)) fn.mockReset();
  api.getAuthorizationMe.mockResolvedValue({
    id: "me",
    roles: ["Owner"],
    permissions: ["*"],
    version: "1",
    canManageAuthorization: true,
    administrationScope: { isOwner: true, delegationScopes: [] },
  });
  api.listPermissionCatalog.mockResolvedValue(catalog);
  api.listAuthorizationRoles.mockResolvedValue(roles);
  api.listProvisioningRoles.mockResolvedValue({
    roles: [
      { id: "r-employee", name: "employee", displayName: "Employee", permissionKeys: ["staff:access", "time:access"] },
    ],
  });
});

describe("the roles page's Roles Staff may give", () => {
  it("offers the custom roles alone, each with its permissions, the set ticked", async () => {
    const card = await section();
    const employee = await within(card).findByRole("checkbox", { name: "Employee" });
    await waitFor(() => expect(employee).toBeChecked());
    expect(within(card).getByRole("checkbox", { name: "Timekeeper" })).not.toBeChecked();
    expect(within(card).queryByRole("checkbox", { name: "User" })).not.toBeInTheDocument();
    const employeeRow = employee.closest("[data-role-row]") as HTMLElement;
    expect(within(employeeRow).getByText("Use Staff")).toBeInTheDocument();
    expect(within(employeeRow).getByText("Use Time")).toBeInTheDocument();
  });

  it("marks a role that grants administration, by Staff's keys or a key that cannot be delegated", async () => {
    const card = await section();
    for (const name of ["HR lead", "Meter reader"]) {
      const row = (await within(card).findByRole("checkbox", { name })).closest("[data-role-row]") as HTMLElement;
      expect(within(row).getByText("Grants administration")).toBeInTheDocument();
    }
    const timer = within(card).getByRole("checkbox", { name: "Timekeeper" }).closest("[data-role-row]") as HTMLElement;
    expect(within(timer).queryByText("Grants administration")).not.toBeInTheDocument();
  });

  it("saves the whole set: the roles kept and the role added", async () => {
    api.replaceProvisioningRoles.mockResolvedValue({ roles: [] });
    const card = await section();
    await waitFor(() => expect(within(card).getByRole("checkbox", { name: "Employee" })).toBeChecked());
    fireEvent.click(within(card).getByRole("checkbox", { name: "Timekeeper" }));
    fireEvent.click(within(card).getByRole("button", { name: "Save roles Staff may give" }));
    await waitFor(() => expect(api.replaceProvisioningRoles).toHaveBeenCalledWith(["r-employee", "r-timer"]));
    expect(await screen.findByText("Roles Staff may give saved")).toBeInTheDocument();
  });

  it("saves an emptied set as an empty set", async () => {
    api.replaceProvisioningRoles.mockResolvedValue({ roles: [] });
    const card = await section();
    await waitFor(() => expect(within(card).getByRole("checkbox", { name: "Employee" })).toBeChecked());
    fireEvent.click(within(card).getByRole("checkbox", { name: "Employee" }));
    expect(within(card).getByText("No role named: Staff creates no accounts until you name one.")).toBeInTheDocument();
    fireEvent.click(within(card).getByRole("button", { name: "Save roles Staff may give" }));
    await waitFor(() => expect(api.replaceProvisioningRoles).toHaveBeenCalledWith([]));
  });

  it("puts role_not_assignable in words, naming the role and the key that grants administration", async () => {
    api.replaceProvisioningRoles.mockRejectedValue(refusal(400, "role_not_assignable"));
    const card = await section();
    await waitFor(() => expect(within(card).getByRole("checkbox", { name: "Employee" })).toBeChecked());
    fireEvent.click(within(card).getByRole("checkbox", { name: "HR lead" }));
    fireEvent.click(within(card).getByRole("button", { name: "Save roles Staff may give" }));
    expect(
      await screen.findByText(
        "HR lead carries Manage staff (staff:manage), which grants administration; Staff may not give it.",
      ),
    ).toBeInTheDocument();
    expect(screen.queryByText("server text for role_not_assignable")).not.toBeInTheDocument();
  });

  it("names a non-delegable key the same way", async () => {
    api.replaceProvisioningRoles.mockRejectedValue(refusal(400, "role_not_assignable"));
    const card = await section();
    await waitFor(() => expect(within(card).getByRole("checkbox", { name: "Employee" })).toBeChecked());
    fireEvent.click(within(card).getByRole("checkbox", { name: "Meter reader" }));
    fireEvent.click(within(card).getByRole("button", { name: "Save roles Staff may give" }));
    expect(
      await screen.findByText(
        "Meter reader carries Secret energy (energy:secret), which grants administration; Staff may not give it.",
      ),
    ).toBeInTheDocument();
  });

  it("says only custom roles may be given when no chosen role carries such a key", async () => {
    api.replaceProvisioningRoles.mockRejectedValue(refusal(400, "role_not_assignable"));
    const card = await section();
    await waitFor(() => expect(within(card).getByRole("checkbox", { name: "Employee" })).toBeChecked());
    fireEvent.click(within(card).getByRole("button", { name: "Save roles Staff may give" }));
    expect(await screen.findByText("Only custom roles can be roles Staff may give.")).toBeInTheDocument();
  });
});

describe("the role editor", () => {
  it("puts role_provisioning_protected in words", async () => {
    api.updateAuthorizationRole.mockRejectedValue(refusal(403, "role_provisioning_protected"));
    await renderRoles();
    const card = (await screen.findByRole("heading", { name: "Role permissions" })).closest(
      ".mantine-Card-root",
    ) as HTMLElement;
    const employee = within(card).getByText("Employee").closest(".mantine-Card-root") as HTMLElement;
    fireEvent.click(within(employee).getByRole("button", { name: "Edit" }));
    fireEvent.click(await screen.findByRole("button", { name: "Save role" }));
    await waitFor(() => expect(api.updateAuthorizationRole).toHaveBeenCalled());
    expect(
      await screen.findByText(
        "This role is one Staff may give, so it cannot receive a permission that grants administration. Take it out of Roles Staff may give first.",
      ),
    ).toBeInTheDocument();
    expect(screen.queryByText("server text for role_provisioning_protected")).not.toBeInTheDocument();
  });
});
