import { MantineProvider } from "@mantine/core";
import { render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { AppShellLayout } from "./app-shell-layout";

afterEach(() => {
  delete window.__VANTIGO_APP__;
});

describe("AppShellLayout", () => {
  it("renders no sidebar and no burger when no nav is given, and the content still", () => {
    render(
      <MantineProvider env="test">
        <AppShellLayout>
          <div>content</div>
        </AppShellLayout>
      </MantineProvider>,
    );
    expect(screen.queryByRole("navigation", { name: "Primary navigation" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Open navigation" })).not.toBeInTheDocument();
    expect(screen.getByText("content")).toBeInTheDocument();
  });

  it("renders the sidebar and the burger when nav is given", () => {
    render(
      <MantineProvider env="test">
        <AppShellLayout nav={() => <a href="/customers">Customers</a>}>
          <div>content</div>
        </AppShellLayout>
      </MantineProvider>,
    );
    expect(screen.getByRole("navigation", { name: "Primary navigation" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Open navigation" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Customers" })).toBeInTheDocument();
  });

  it("places the header slots in the banner and shows the app title over the product title", () => {
    window.__VANTIGO_APP__ = { basePath: "/", title: "Acme ERP", support: {} };
    render(
      <MantineProvider env="test">
        <AppShellLayout
          title="Customers"
          headerCenter={<span>search here</span>}
          headerActions={<button type="button">act</button>}
        >
          <div>content</div>
        </AppShellLayout>
      </MantineProvider>,
    );
    const header = screen.getByRole("banner");
    expect(header).toHaveTextContent("Customers");
    expect(screen.queryByRole("heading", { name: "Acme ERP" })).not.toBeInTheDocument();
    expect(header).toHaveTextContent("search here");
    expect(header).toContainElement(screen.getByRole("button", { name: "act" }));
  });

  it("links the logo to the dashboard through the host's link component", () => {
    const HostLink = ({ to, children, ...rest }: { to: string; children?: React.ReactNode }) => (
      <a href={to} data-host-link {...rest}>
        {children}
      </a>
    );
    render(
      <MantineProvider env="test">
        <AppShellLayout linkComponent={HostLink}>
          <div>content</div>
        </AppShellLayout>
      </MantineProvider>,
    );
    const home = screen.getByRole("link", { name: "Go to the dashboard" });
    expect(home).toHaveAttribute("href", "/dashboard");
    expect(home).toHaveAttribute("data-host-link");
    expect(screen.getByRole("banner")).toContainElement(home);
  });

  it("links the logo to the dashboard with a plain anchor when no link component is given", () => {
    render(
      <MantineProvider env="test">
        <AppShellLayout>
          <div>content</div>
        </AppShellLayout>
      </MantineProvider>,
    );
    expect(screen.getByRole("link", { name: "Go to the dashboard" })).toHaveAttribute("href", "/dashboard");
  });
});
