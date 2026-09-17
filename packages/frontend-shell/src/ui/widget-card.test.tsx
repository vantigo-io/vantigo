import { MantineProvider } from "@mantine/core";
import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { WidgetCard } from "./widget-card";

afterEach(() => {
  vi.restoreAllMocks();
});

const wrap = (ui: React.ReactNode) => render(<MantineProvider env="test">{ui}</MantineProvider>);

describe("WidgetCard's boundary", () => {
  it("renders the body when nothing throws", () => {
    wrap(
      <WidgetCard title="New customers">
        <p>a chart</p>
      </WidgetCard>,
    );

    expect(screen.getByText("New customers")).toBeInTheDocument();
    expect(screen.getByText("a chart")).toBeInTheDocument();
  });

  it("keeps the title and shows the widget's own error state when the body throws", () => {
    vi.spyOn(console, "error").mockImplementation(() => undefined);
    const Thrower = (): never => {
      throw new Error("chart exploded");
    };

    wrap(
      <WidgetCard title="New customers">
        <Thrower />
      </WidgetCard>,
    );

    expect(screen.getByText("New customers")).toBeInTheDocument();
    expect(screen.getByRole("alert")).toHaveTextContent("This widget could not be shown.");
    expect(screen.getByRole("button", { name: "Try again" })).toBeInTheDocument();
  });

  it("re-mounts the body on retry", () => {
    vi.spyOn(console, "error").mockImplementation(() => undefined);
    // Throws on every render until the test flips it: React itself retries a
    // failed concurrent render once, so a single-shot throw never reaches the boundary.
    let broken = true;
    const Flaky = () => {
      if (broken) throw new Error("still failing");
      return <p>recovered</p>;
    };

    wrap(
      <WidgetCard title="New customers">
        <Flaky />
      </WidgetCard>,
    );
    expect(screen.getByRole("alert")).toBeInTheDocument();

    broken = false;
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));

    expect(screen.getByText("recovered")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
});
