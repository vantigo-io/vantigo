import { MantineProvider } from "@mantine/core";
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ContentSkeleton } from "./content-skeleton";
import { EmptyState } from "./empty-state";

describe("EmptyState", () => {
  it("renders the title, description and action", () => {
    render(
      <MantineProvider env="test">
        <EmptyState
          title="No customers found"
          description="Try another search."
          action={<button type="button">Reset</button>}
        />
      </MantineProvider>,
    );

    expect(screen.getByText("No customers found")).toBeInTheDocument();
    expect(screen.getByText("Try another search.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Reset" })).toBeInTheDocument();
  });

  it("renders the icon when given", () => {
    const Icon = ({ size }: { size?: number }) => <svg data-testid="icon" width={size} />;
    render(
      <MantineProvider env="test">
        <EmptyState icon={Icon} title="Nothing here" />
      </MantineProvider>,
    );

    expect(screen.getByTestId("icon")).toHaveAttribute("width", "38");
  });
});

describe("ContentSkeleton", () => {
  it("renders the requested number of rows and marks itself busy", () => {
    render(
      <MantineProvider env="test">
        <ContentSkeleton rows={4} />
      </MantineProvider>,
    );

    const skeleton = screen.getByTestId("content-skeleton");
    expect(skeleton).toHaveAttribute("aria-busy", "true");
    expect(skeleton.childElementCount).toBe(4);
  });
});
