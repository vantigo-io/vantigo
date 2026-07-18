import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { render } from "../test-utils";
import { StatCard } from "./stat-card";

describe("StatCard", () => {
  it("renders label and value", () => {
    render(<StatCard label="Total customers" value="1 204" />);
    expect(screen.getByText("Total customers")).toBeInTheDocument();
    expect(screen.getByText("1 204")).toBeInTheDocument();
  });

  it("renders positive diff with plus sign", () => {
    render(<StatCard label="Revenue" value="kr 84 500" diff={12.3} />);
    expect(screen.getByText("+12.3%")).toBeInTheDocument();
  });

  it("renders negative diff with minus sign", () => {
    render(<StatCard label="Revenue" value="kr 84 500" diff={-4.1} />);
    expect(screen.getByText("-4.1%")).toBeInTheDocument();
  });

  it("treats zero diff as positive", () => {
    render(<StatCard label="Revenue" value="0" diff={0} />);
    expect(screen.getByText("+0.0%")).toBeInTheDocument();
  });

  it("renders no diff badge when diff is omitted", () => {
    render(<StatCard label="Projects" value="7" />);
    expect(screen.queryByText(/%/)).not.toBeInTheDocument();
  });

  it("renders description when provided", () => {
    render(<StatCard label="Projects" value="7" description="Compared to previous month" />);
    expect(screen.getByText("Compared to previous month")).toBeInTheDocument();
  });

  it("omits description when not provided", () => {
    render(<StatCard label="Projects" value="7" />);
    expect(screen.queryByText("Compared to previous month")).not.toBeInTheDocument();
  });
});
