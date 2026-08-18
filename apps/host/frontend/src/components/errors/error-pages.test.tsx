import { MantineProvider } from "@mantine/core";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import "../../i18n";
import { DevErrorDetails } from "./dev-error-details";
import { ErrorPage } from "./error-page";
import { ForbiddenPage } from "./forbidden";
import { MaintenancePage } from "./maintenance";
import { NotFoundPage } from "./not-found";

const back = vi.fn();
vi.mock("@tanstack/react-router", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-router")>();
  return {
    ...actual,
    Link: ({ children }: { children: React.ReactNode }) => <a href="/">{children}</a>,
    useRouter: () => ({ history: { back } }),
  };
});

const renderPage = (ui: React.ReactNode) => render(<MantineProvider>{ui}</MantineProvider>);

describe("ErrorPage", () => {
  it("renders title, message and default actions", async () => {
    renderPage(
      <ErrorPage illustration={<svg role="img" aria-label="art" />} title="Broken" message="Something broke" />,
    );
    expect(screen.getByRole("heading", { name: "Broken" })).toBeInTheDocument();
    expect(screen.getByText("Something broke")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Go home" })).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Go back" }));
    expect(back).toHaveBeenCalled();
  });

  it("reveals technical details with error id on toggle", async () => {
    renderPage(
      <ErrorPage
        illustration={null}
        title="Broken"
        message="Something broke"
        error={new Error("kaboom")}
        errorId="abc-123"
      />,
    );
    await userEvent.click(screen.getByText("Technical details"));
    expect(screen.getByText(/abc-123/)).toBeInTheDocument();
    expect(screen.getByText(/kaboom/)).toBeInTheDocument();
  });
});

describe("DevErrorDetails", () => {
  it("shows stack trace and context in dev mode", () => {
    const error = new Error("dev failure");
    renderPage(<DevErrorDetails error={error} context={{ attemptedPathname: "/nowhere" }} />);
    expect(screen.getByText("DEV")).toBeInTheDocument();
    expect(screen.getAllByText(/dev failure/).length).toBeGreaterThan(0);
    expect(screen.getByText("attemptedPathname")).toBeInTheDocument();
    expect(screen.getByText("/nowhere")).toBeInTheDocument();
  });
});

describe("NotFoundPage", () => {
  it("renders the not found title", () => {
    renderPage(<NotFoundPage />);
    expect(screen.getByRole("heading", { name: "Page not found" })).toBeInTheDocument();
  });
});

describe("ForbiddenPage", () => {
  it("renders access denied with contact-admin alert", () => {
    renderPage(<ForbiddenPage requiredModule="customers" />);
    expect(screen.getByRole("heading", { name: "Access denied" })).toBeInTheDocument();
    expect(screen.getByText(/administrator/i)).toBeInTheDocument();
  });
});

describe("MaintenancePage", () => {
  it("renders the custom plain-text message when provided", () => {
    renderPage(<MaintenancePage message={"We are upgrading.\nBack at 14:00."} />);
    expect(screen.getByRole("heading", { name: "Temporarily unavailable" })).toBeInTheDocument();
    expect(screen.getByText(/We are upgrading/)).toBeInTheDocument();
  });

  it("falls back to default copy without a message", () => {
    renderPage(<MaintenancePage />);
    expect(screen.getByText(/undergoing maintenance/i)).toBeInTheDocument();
  });
});
