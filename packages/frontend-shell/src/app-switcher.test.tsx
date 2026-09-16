import { MantineProvider } from "@mantine/core";
import { IconBolt, IconHome, IconUsers } from "@tabler/icons-react";
import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { AppSwitcher } from "./app-switcher";

const open = async () => {
  fireEvent.click(screen.getByRole("button", { name: "Switch app" }));
  return screen.findByRole("dialog");
};

describe("AppSwitcher", () => {
  it("renders nothing without apps", () => {
    render(
      <MantineProvider>
        <AppSwitcher apps={[]} />
      </MantineProvider>,
    );
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });

  it("lists tiles, marks the current one and navigates on select", async () => {
    const onSelect = vi.fn();
    render(
      <MantineProvider>
        <AppSwitcher
          apps={[
            { id: "home", label: "Home", icon: IconHome, onSelect: vi.fn(), current: true },
            { id: "customers", label: "Customers", icon: IconUsers, onSelect },
          ]}
        />
      </MantineProvider>,
    );
    await open();

    const home = screen.getByRole("button", { name: "Home" });
    expect(home).toHaveAttribute("aria-current", "true");
    fireEvent.click(home);

    fireEvent.click(screen.getByRole("button", { name: "Customers" }));
    expect(onSelect).toHaveBeenCalledOnce();
  });

  it("shows a disabled app muted with its reason and never selects it", async () => {
    const onSelect = vi.fn();
    render(
      <MantineProvider>
        <AppSwitcher
          apps={[{ id: "energy", label: "Energy", icon: IconBolt, onSelect, disabledReason: "Not enabled" }]}
        />
      </MantineProvider>,
    );
    await open();

    const tile = screen.getByRole("button", { name: /Energy/ });
    expect(tile).toHaveAttribute("aria-disabled", "true");
    expect(tile).toBeDisabled();
    expect(screen.getByText("Not enabled")).toBeInTheDocument();
    fireEvent.click(tile);
    expect(onSelect).not.toHaveBeenCalled();
  });
});
