import { render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AppErrorBoundary } from "./app-error-boundary";

const Thrower = (): never => {
  throw new Error("provider exploded");
};

afterEach(() => {
  vi.restoreAllMocks();
});

// Rendered deliberately without MantineProvider, the i18n provider or the
// router: that is the situation the boundary exists for.
describe("AppErrorBoundary", () => {
  it("renders its children when nothing throws", () => {
    render(
      <AppErrorBoundary>
        <p>the app</p>
      </AppErrorBoundary>,
    );

    expect(screen.getByText("the app")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("shows the static crash page, with a reload action, when a child throws", () => {
    const consoleError = vi.spyOn(console, "error").mockImplementation(() => undefined);

    render(
      <AppErrorBoundary>
        <Thrower />
      </AppErrorBoundary>,
    );

    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent("Something went wrong");
    expect(screen.getByRole("button", { name: "Reload the page" })).toBeInTheDocument();
    expect(consoleError).toHaveBeenCalledWith(
      "Unrecoverable error outside the router",
      expect.objectContaining({ message: "provider exploded" }),
      expect.any(String),
    );
  });
});
