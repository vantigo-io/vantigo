import { MantineProvider } from "@mantine/core";
import { cleanup, render, screen } from "@testing-library/react";
import { AppShellLayout } from "@vantigo/frontend-shell";
import { afterEach, describe, expect, it } from "vitest";

afterEach(() => {
  cleanup();
  delete window.__VANTIGO_APP__;
});

const renderShell = () =>
  render(
    <MantineProvider>
      <AppShellLayout nav={() => null}>
        <div>content</div>
      </AppShellLayout>
    </MantineProvider>,
  );

describe("AppShellLayout whitelabeling", () => {
  it("shows the bundled Vantigo logo and no footer by default", () => {
    renderShell();

    const logo = screen.getByRole("img", { name: "Vantigo" });
    expect(logo).toHaveAttribute("src", expect.stringContaining("logo"));
    expect(screen.queryByText("Support:")).not.toBeInTheDocument();
  });

  it("uses the injected title as module name and logo alt text", () => {
    window.__VANTIGO_APP__ = { basePath: "/customers/", title: "Acme CRM", support: {} };

    renderShell();

    expect(screen.getByRole("heading", { name: "Acme CRM" })).toBeInTheDocument();
    expect(screen.getByRole("img", { name: "Acme CRM" })).toBeInTheDocument();
  });

  it("replaces the logo with the injected logo URL", () => {
    window.__VANTIGO_APP__ = {
      basePath: "/customers/",
      title: "Acme CRM",
      logoUrl: "https://cdn.acme.test/logo.svg",
      support: {},
    };

    renderShell();

    expect(screen.getByRole("img", { name: "Acme CRM" })).toHaveAttribute("src", "https://cdn.acme.test/logo.svg");
  });

  it("renders the support footer with links when support contact is configured", () => {
    window.__VANTIGO_APP__ = {
      basePath: "/customers/",
      title: "Acme CRM",
      support: { email: "help@acme.test", phone: "+47 123 45 678", url: "https://support.acme.test" },
    };

    renderShell();

    expect(screen.getByText("Support:")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "help@acme.test" })).toHaveAttribute("href", "mailto:help@acme.test");
    expect(screen.getByRole("link", { name: "+47 123 45 678" })).toHaveAttribute("href", "tel:+4712345678");
    expect(screen.getByRole("link", { name: "Help center" })).toHaveAttribute("href", "https://support.acme.test");
  });

  it("renders the footer for a single configured support field", () => {
    window.__VANTIGO_APP__ = {
      basePath: "/customers/",
      title: "Acme CRM",
      support: { email: "help@acme.test" },
    };

    renderShell();

    expect(screen.getByText("Support:")).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Help center" })).not.toBeInTheDocument();
  });
});
