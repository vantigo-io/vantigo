import { screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { projectSummary } from "../test/fixtures";
import { renderWithProviders } from "../test/render";
import { stubTimeApi } from "../test/server";
import { ProjectTimePanel } from "./project-time-panel";
import "../i18n";

describe("ProjectTimePanel", () => {
  it("sums the project's hours by status, by line and by person, with what they bill", async () => {
    stubTimeApi({ projectSummary });
    renderWithProviders(<ProjectTimePanel projectId={1001} />);

    expect(await screen.findByTestId("project-time-total")).toHaveTextContent("30.5 h");

    const statuses = screen.getByTestId("project-time-statuses");
    expect(statuses).toHaveTextContent("Approved");
    expect(statuses).toHaveTextContent("20");
    expect(statuses).toHaveTextContent("Rejected");
    // A status with nothing in it is left out rather than shown as a zero.
    expect(statuses).not.toHaveTextContent("Invoiced");

    const lines = screen.getByTestId("project-time-by-line");
    expect(within(lines).getByText("KVEM1000-PM").closest("tr")).toHaveTextContent("10.5");
    expect(within(lines).getByText("No line").closest("tr")).toHaveTextContent("2");

    const people = screen.getByTestId("project-time-by-person");
    expect(within(people).getByText("Ada Lovelace").closest("tr")).toHaveTextContent("22.5");

    expect(screen.getByTestId("project-time-billing")).toHaveTextContent("33,600.00");
    expect(screen.getByTestId("project-time-billing")).toHaveTextContent("2 h without a rate");
    expect(screen.getByRole("link", { name: "Log time" })).toHaveAttribute("href", "/time");
  });

  it("leaves the money out for a caller who may not see it", async () => {
    stubTimeApi({ projectSummary: { ...projectSummary, billing: undefined } });
    renderWithProviders(<ProjectTimePanel projectId={1001} />);

    expect(await screen.findByTestId("project-time-total")).toHaveTextContent("30.5 h");
    expect(screen.queryByTestId("project-time-billing")).not.toBeInTheDocument();
  });

  it("shows nothing to show when the project has no time summary", async () => {
    stubTimeApi({});
    renderWithProviders(<ProjectTimePanel projectId={4242} />);

    expect(await screen.findByText("No time logged on this project")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
});
