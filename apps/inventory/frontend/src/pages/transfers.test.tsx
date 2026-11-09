import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { choose, sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { stubInventoryApi, transferFixture, transferLine, variants } from "../test/server";

const [tsM, , bolt] = variants();

const fixtures = () => [
  transferFixture({ id: 1, number: 1001, status: "draft", lines: [transferLine(1, tsM, 2)] }),
  transferFixture({
    id: 2,
    number: 1002,
    status: "sent",
    reference: "overdue",
    expectedArrival: "2026-11-01",
    lines: [transferLine(2, bolt, 5, { sent: 5 })],
  }),
  transferFixture({
    id: 3,
    number: 1003,
    status: "received",
    lines: [transferLine(3, tsM, 1, { sent: 1, received: 1 })],
  }),
];

describe("the transfers list", () => {
  it("shows the open transfers by default, with the overdue mark", async () => {
    const fetchMock = stubInventoryApi({ transfers: fixtures() });
    renderRoute("/inventory/transfers");
    expect(await screen.findByRole("link", { name: "Transfer 1001" })).toHaveAttribute(
      "href",
      "/inventory/transfers/1",
    );
    expect(screen.getByRole("link", { name: "Transfer 1002" })).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Transfer 1003" })).not.toBeInTheDocument();
    expect(sent(fetchMock, "GET", /\/transfers\?status=open$/)).toHaveLength(1);
    const overdue = screen.getByRole("link", { name: "Transfer 1002" }).closest("tr") as HTMLElement;
    expect(within(overdue).getByLabelText("Overdue")).toBeInTheDocument();
    expect(within(overdue).getByText("Sent")).toBeInTheDocument();
  });

  it("filters by status, site and a SKU, in the URL", async () => {
    const fetchMock = stubInventoryApi({ transfers: fixtures() });
    const { router } = renderRoute("/inventory/transfers");
    await screen.findByRole("link", { name: "Transfer 1001" });
    await choose(document.body, "Status", "All statuses");
    expect(await screen.findByRole("link", { name: "Transfer 1003" })).toBeInTheDocument();
    await choose(document.body, "To site", "BGO · Bergen butikk");
    await waitFor(() => expect(router.state.location.search).toEqual({ status: "all", toSiteId: 2 }));
    await userEvent.type(screen.getByRole("textbox", { name: "Search" }), "bolt-10{Enter}");
    await waitFor(() => expect(screen.queryByRole("link", { name: "Transfer 1001" })).not.toBeInTheDocument());
    expect(screen.getByRole("link", { name: "Transfer 1002" })).toBeInTheDocument();
    expect(sent(fetchMock, "GET", /\/transfers\?toSiteId=2&q=bolt-10$/)).toHaveLength(1);
  });

  it("offers New transfer only to a caller who may transfer", async () => {
    stubInventoryApi({ transfers: fixtures(), meta: { canTransfer: false } });
    renderRoute("/inventory/transfers");
    await screen.findByRole("link", { name: "Transfer 1001" });
    expect(screen.queryByRole("link", { name: "New transfer" })).not.toBeInTheDocument();
  });

  it("opens New transfer with the sites the list is filtered by", async () => {
    stubInventoryApi({ transfers: fixtures() });
    renderRoute("/inventory/transfers?fromSiteId=1&toSiteId=2");
    expect(await screen.findByRole("link", { name: "New transfer" })).toHaveAttribute(
      "href",
      "/inventory/transfers/new?fromSiteId=1&toSiteId=2",
    );
  });
});
