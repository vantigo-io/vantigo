import { MantineProvider, Tabs } from "@mantine/core";
import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { PageTabs } from "./page-tabs";

const items = [
  { value: "overview", label: "Overview" },
  { value: "energy", label: "Energy" },
] as const;

describe("PageTabs", () => {
  it("renders the items as tabs and marks the given value selected", () => {
    render(
      <MantineProvider env="test">
        <PageTabs items={items} value="energy" onChange={vi.fn()} aria-label="Customer views" />
      </MantineProvider>,
    );

    const list = screen.getByRole("tablist", { name: "Customer views" });
    expect(list).toBeInTheDocument();
    expect(screen.getAllByRole("tab").map((tab) => tab.textContent)).toEqual(["Overview", "Energy"]);
    expect(screen.getByRole("tab", { name: "Energy" })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByRole("tab", { name: "Overview" })).toHaveAttribute("aria-selected", "false");
  });

  it("reports the clicked value and leaves navigation to the caller", () => {
    const onChange = vi.fn();
    render(
      <MantineProvider env="test">
        <PageTabs items={items} value="overview" onChange={onChange} />
      </MantineProvider>,
    );

    fireEvent.click(screen.getByRole("tab", { name: "Energy" }));

    expect(onChange).toHaveBeenCalledWith("energy");
    // Controlled: the selection does not move until the caller re-renders with the new value.
    expect(screen.getByRole("tab", { name: "Overview" })).toHaveAttribute("aria-selected", "true");
  });

  it("renders only the selected panel when panels are given as children", () => {
    render(
      <MantineProvider env="test">
        <PageTabs items={items} value="energy" onChange={vi.fn()}>
          <Tabs.Panel value="overview">overview body</Tabs.Panel>
          <Tabs.Panel value="energy">energy body</Tabs.Panel>
        </PageTabs>
      </MantineProvider>,
    );

    expect(screen.getByText("energy body")).toBeInTheDocument();
    expect(screen.queryByText("overview body")).not.toBeInTheDocument();
  });
});
