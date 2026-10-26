import { screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { allocation, weekEnd } from "../test/fixtures";
import { renderRoute } from "../test/route-tree";
import { stubPlanningApi } from "../test/server";

describe("MyPlanningPage — on a phone", () => {
  // A phone is narrower than Mantine's `sm` (48em): every max-width query at
  // or above it matches, and nothing wider does.
  const phone = (query: string) => {
    const width = /max-width:\s*([\d.]+)em/.exec(query);
    return width !== null && Number(width[1]) >= 23.4375;
  };
  const original = window.matchMedia;

  afterEach(() => {
    window.matchMedia = original;
    window.innerWidth = 1024;
  });

  const narrow = () => {
    window.innerWidth = 375;
    window.matchMedia = vi.fn((query: string) => ({
      matches: phone(query),
      media: query,
      onchange: null,
      addListener: vi.fn(),
      removeListener: vi.fn(),
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      dispatchEvent: vi.fn(),
    }));
  };

  it("lists my confirmed bookings as cards, never a tentative one", async () => {
    narrow();
    stubPlanningApi({
      role: "access",
      allocations: [
        allocation({ id: 501, personId: 1, projectId: 100, title: "Frontend developer", note: "Planners only" }),
        allocation({ id: 502, personId: 1, projectId: 200, status: "tentative" }),
        allocation({ id: 503, personId: 2, projectId: 100 }),
        allocation({ id: 504, personId: 1, projectId: 100, load: 16, loadUnit: "hours_per_week", toDate: weekEnd(2) }),
      ],
    });
    renderRoute("/planning/me");

    const cards = await screen.findAllByRole("listitem");
    expect(cards).toHaveLength(2);
    expect(cards[0]).toHaveTextContent("P-100 Harbour app");
    expect(cards[0]).toHaveTextContent("Frontend developer");
    expect(cards[0]).toHaveTextContent("50%");
    expect(within(cards[1]).getByText("16 h/week")).toBeInTheDocument();
    expect(screen.queryByText("P-200 Tender bid")).not.toBeInTheDocument();
    expect(screen.queryByText("Planners only")).not.toBeInTheDocument();
  });

  it("says why there is nothing when Staff links me to nobody", async () => {
    narrow();
    stubPlanningApi({ role: "access", me: null });
    renderRoute("/planning/me");
    expect(await screen.findByText("No person is linked to you")).toBeInTheDocument();
    expect(screen.getByText(/Ask HR to link your user to your record/)).toBeInTheDocument();
  });

  it("says when nothing is booked ahead", async () => {
    stubPlanningApi({ role: "access", allocations: [] });
    renderRoute("/planning/me");
    expect(await screen.findByText("Nothing booked ahead")).toBeInTheDocument();
  });
});
