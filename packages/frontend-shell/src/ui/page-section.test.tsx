import { MantineProvider } from "@mantine/core";
import { render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { describe, expect, it } from "vitest";
import { PageSection } from "./page-section";

const wrap = (ui: ReactNode) => render(<MantineProvider env="test">{ui}</MantineProvider>);

describe("PageSection", () => {
  it("renders its content on a bordered card with the page padding", () => {
    wrap(
      <PageSection data-testid="surface">
        <p>a table</p>
      </PageSection>,
    );

    const surface = screen.getByTestId("surface");
    expect(surface).toHaveClass("mantine-Card-root");
    expect(surface).toHaveAttribute("data-with-border", "true");
    expect(surface.style.getPropertyValue("--card-padding")).toBe("var(--mantine-spacing-lg)");
    expect(surface.style.getPropertyValue("--paper-radius")).toBe("var(--mantine-radius-md)");
    expect(surface.tagName).toBe("DIV");
    expect(screen.getByText("a table")).toBeInTheDocument();
  });

  it("names a titled section by its heading and shows the actions", () => {
    wrap(
      <PageSection title="Open orders" actions={<button type="button">Export</button>}>
        <p>rows</p>
      </PageSection>,
    );

    const region = screen.getByRole("region", { name: "Open orders" });
    expect(region.tagName).toBe("SECTION");
    expect(screen.getByRole("heading", { level: 2, name: "Open orders" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Export" })).toBeInTheDocument();
  });

  it("runs edge to edge with padding 0 and keeps the border", () => {
    wrap(
      <PageSection padding={0} data-testid="surface">
        <p>a grid</p>
      </PageSection>,
    );

    const surface = screen.getByTestId("surface");
    expect(surface.style.getPropertyValue("--card-padding")).toBe("0rem");
    expect(surface).toHaveAttribute("data-with-border", "true");
  });

  it("passes the class name, element and an explicit label through", () => {
    wrap(
      <PageSection component="section" aria-label="Filters" className="extra" title="Ignored as name">
        <p>filters</p>
      </PageSection>,
    );

    const region = screen.getByRole("region", { name: "Filters" });
    expect(region).toHaveClass("extra");
    expect(region).not.toHaveAttribute("aria-labelledby");
  });
});
