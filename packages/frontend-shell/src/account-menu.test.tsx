import { MantineProvider } from "@mantine/core";
import { IconSettings, IconShield } from "@tabler/icons-react";
import { fireEvent, render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { AccountMenu } from "./account-menu";

const user = { displayName: "Ada Lovelace", email: "ada@acme.test" };

const open = async () => {
  fireEvent.click(screen.getByRole("button", { name: "Open account menu" }));
  return screen.findByRole("menu");
};

describe("AccountMenu", () => {
  it("shows the user, the given sections in order, and sign out last", async () => {
    const onSettings = vi.fn();
    const onSignOut = vi.fn();
    render(
      <MantineProvider env="test">
        <AccountMenu
          user={user}
          sections={[
            { label: "Your account", items: [{ label: "Settings", icon: IconSettings, onSelect: onSettings }] },
            { label: "Workspace", items: [{ label: "Roles and access", icon: IconShield, onSelect: vi.fn() }] },
          ]}
          onSignOut={onSignOut}
        />
      </MantineProvider>,
    );
    expect(screen.getByText("AL")).toBeInTheDocument();

    const menu = await open();
    expect(within(menu).getByText("Ada Lovelace")).toBeInTheDocument();
    expect(within(menu).getByText("ada@acme.test")).toBeInTheDocument();
    expect(
      within(menu)
        .getAllByRole("menuitem")
        .map((item) => item.textContent),
    ).toEqual(["Settings", "Roles and access", "Sign out"]);
    const labels = within(menu)
      .getAllByText(/Your account|Workspace/)
      .map((node) => node.textContent);
    expect(labels).toEqual(["Your account", "Workspace"]);

    fireEvent.click(within(menu).getByRole("menuitem", { name: "Settings" }));
    expect(onSettings).toHaveBeenCalledOnce();
  });

  it("signs out, unless sign-out is disabled", async () => {
    const onSignOut = vi.fn();
    const { rerender } = render(
      <MantineProvider env="test">
        <AccountMenu user={user} sections={[]} onSignOut={onSignOut} />
      </MantineProvider>,
    );
    let menu = await open();
    fireEvent.click(within(menu).getByRole("menuitem", { name: "Sign out" }));
    expect(onSignOut).toHaveBeenCalledOnce();

    rerender(
      <MantineProvider env="test">
        <AccountMenu user={user} sections={[]} onSignOut={onSignOut} signOutDisabled />
      </MantineProvider>,
    );
    menu = await open();
    expect(within(menu).getByRole("menuitem", { name: "Sign out" })).toHaveAttribute("data-disabled");
  });

  it("renders a placeholder while the user is loading", () => {
    render(
      <MantineProvider env="test">
        <AccountMenu user={undefined} sections={[]} onSignOut={vi.fn()} />
      </MantineProvider>,
    );
    expect(screen.getByRole("button", { name: "Open account menu" })).toBeInTheDocument();
  });
});
