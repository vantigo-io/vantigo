import { MantineProvider } from "@mantine/core";
import { render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import "../i18n";
import { AppLayout } from "./-app-layout";

vi.mock("@tanstack/react-router", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-router")>();
  return {
    ...actual,
    Outlet: () => <div>app content</div>,
    Link: ({ to, children }: { to: string; children: React.ReactNode }) => <a href={to}>{children}</a>,
    useRouter: () => ({ history: { back: vi.fn() } }),
  };
});

const inject = (modules: string[]) => {
  window.__VANTIGO_APP__ = { basePath: "/", title: "Vantigo", support: {}, modules };
};

afterEach(() => {
  delete window.__VANTIGO_APP__;
});

const renderLayout = () =>
  render(
    <MantineProvider>
      <AppLayout app="customers" />
    </MantineProvider>,
  );

describe("AppLayout", () => {
  it("renders the app's routes when its module is enabled", () => {
    inject(["customers", "energy"]);
    renderLayout();
    expect(screen.getByText("app content")).toBeInTheDocument();
  });

  it("renders every module's routes when nothing was injected (dev server)", () => {
    renderLayout();
    expect(screen.getByText("app content")).toBeInTheDocument();
  });

  it("renders the not-enabled page, naming the app, when its module is off", () => {
    inject(["energy"]);
    renderLayout();
    expect(screen.getByRole("heading", { name: "Customers is not enabled" })).toBeInTheDocument();
    expect(screen.getByText(/not enabled in this installation/i)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Go to dashboard" })).toHaveAttribute("href", "/dashboard");
    expect(screen.queryByText("app content")).not.toBeInTheDocument();
  });
});
