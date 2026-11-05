import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { jsonResponse, path, sent } from "../test/api";
import { stubFetch } from "../test/fetch";
import { renderAtHost } from "../test/route-tree";
import { NewBillingRunModal } from "./-new-billing-run-modal";

const run = {
  id: 8,
  through: "2026-09-30",
  issue: false,
  source: "orders",
  combine: true,
  status: "queued",
  customers: 1,
  counts: { queued: 1, drafted: 0, issued: 0, skipped: 0, failed: 0, cancelled: 0 },
  createdAt: "2026-09-12T08:00:00Z",
  createdByUserId: "00000000-0000-0000-0000-000000000001",
};

describe("a billing run of orders", () => {
  it("offers the orders source only with Orders on, and sends it with combine", async () => {
    const fetchMock = stubFetch((input: RequestInfo | URL, init?: RequestInit) =>
      path(input) === "/api/v1/invoices/billing-runs" && init?.method === "POST"
        ? jsonResponse(201, run)
        : new Response(null, { status: 404 }),
    );
    renderAtHost(<NewBillingRunModal today="2026-09-12" canIssue={false} ordersEnabled onClose={vi.fn()} />);

    await userEvent.click(await screen.findByRole("radio", { name: "Orders" }));
    expect(screen.getByRole("textbox", { name: "Orders delivered on or before" })).toBeInTheDocument();
    await userEvent.click(screen.getByRole("checkbox", { name: "Combine a customer's orders (combined invoice)" }));
    await userEvent.click(screen.getByRole("button", { name: "Start the run" }));

    await waitFor(() =>
      expect(sent(fetchMock, "POST").body).toEqual({
        issue: false,
        source: "orders",
        combine: true,
        through: "2026-09-12",
      }),
    );
  });

  it("is consumption only while Orders is off", async () => {
    stubFetch(() => new Response(null, { status: 404 }));
    renderAtHost(<NewBillingRunModal today="2026-09-12" canIssue={false} onClose={vi.fn()} />);

    expect(await screen.findByRole("button", { name: "Start the run" })).toBeInTheDocument();
    expect(screen.queryByRole("radio", { name: "Orders" })).not.toBeInTheDocument();
  });
});
