import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { candidates, request } from "../test/fixtures";
import { renderRoute } from "../test/route-tree";
import { stubPlanningApi } from "../test/server";

describe("IntakePage — the pipeline", () => {
  it("shows each planned project's figures and each request's coverage, the key person flagged", async () => {
    stubPlanningApi({ role: "view" });
    renderRoute("/planning/intake");

    const tender = await screen.findByRole("region", { name: "P-200 Tender bid" });
    expect(screen.queryByRole("region", { name: "P-100 Harbour app" })).not.toBeInTheDocument();
    // 30 h/week requested; Grace's tentative 40% of 37.5 h.
    expect(tender).toHaveTextContent("Requested30 h/week");
    expect(tender).toHaveTextContent("Tentative15 h/week");
    expect(tender).toHaveTextContent("Booked0 h/week");
    const row = within(tender).getByText("Backend developer").closest("tr") as HTMLElement;
    expect(row).toHaveTextContent("Covered");
    expect(row).toHaveTextContent("Key person");
    expect(row).toHaveTextContent("Candidates who cover it: 1");
    expect(within(tender).queryByRole("button", { name: /^Candidates for/ })).not.toBeInTheDocument();
  });

  it("flags no key person when two candidates cover a request", async () => {
    stubPlanningApi({
      role: "view",
      candidates: candidates().map((candidate) =>
        candidate.person.displayName === "Linus Torvalds" ? { ...candidate, covers: true } : candidate,
      ),
    });
    renderRoute("/planning/intake");
    const tender = await screen.findByRole("region", { name: "P-200 Tender bid" });
    const row = within(tender).getByText("Backend developer").closest("tr") as HTMLElement;
    expect(row).toHaveTextContent("Covered");
    expect(row).toHaveTextContent("Candidates who cover it: 2");
    expect(row).not.toHaveTextContent("Key person");
  });

  it("says uncovered when nobody is a candidate, and opens a request's candidates for a planner", async () => {
    stubPlanningApi({
      candidates: [],
      requests: [request({ id: 810, projectId: 200, title: "Architect" })],
    });
    renderRoute("/planning/intake");
    const tender = await screen.findByRole("region", { name: "P-200 Tender bid" });
    const row = within(tender).getByText("Architect").closest("tr") as HTMLElement;
    expect(row).toHaveTextContent("Uncovered");
    expect(row).not.toHaveTextContent("Key person");

    await userEvent.click(within(row).getByRole("button", { name: "Candidates for Architect" }));
    const panel = await screen.findByRole("dialog", { name: "Candidates — Architect" });
    expect(await within(panel).findByText("Nobody fits this request")).toBeInTheDocument();
  });
});
