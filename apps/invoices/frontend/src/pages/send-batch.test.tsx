import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { SendBatchDetail } from "../api/send-batches";
import { jsonResponse, sent } from "../test/api";
import { billingServer } from "../test/billing-server";
import { stubFetch } from "../test/fetch";
import { billingRun, listPage, meta } from "../test/fixtures";
import { renderRoute } from "../test/route-tree";

const path = (input: RequestInfo | URL) => String(input);

const counts = (over: Partial<SendBatchDetail["batch"]["counts"]> = {}) => ({
  queued: 0,
  sending: 0,
  sent: 0,
  handedOver: 0,
  awaitingPrint: 0,
  printed: 0,
  skipped: 0,
  failed: 0,
  uncertain: 0,
  ...over,
});

/** Batch 1001: one e-mailed, one left for printing, one draft skipped and one uncertain. */
const batchDetail = (over: Partial<SendBatchDetail["batch"]> = {}): SendBatchDetail => ({
  batch: {
    id: 1001,
    source: "selection",
    requestedBy: "00000000-0000-4000-8000-000000000001",
    createdAt: "2026-09-12T08:00:00Z",
    finishedAt: "2026-09-12T08:01:00Z",
    counts: counts({ sent: 1, awaitingPrint: 1, skipped: 1, uncertain: 1 }),
    ...over,
  },
  jobs: [
    {
      id: 1,
      invoiceId: 1001,
      number: 1000,
      customerName: "Acme Norge AS",
      channel: "email",
      state: "sent",
      detail: "",
      attempts: 1,
    },
    {
      id: 2,
      invoiceId: 1004,
      number: 1003,
      customerName: "Kari Nordmann",
      channel: "print",
      state: "awaiting_print",
      detail: "",
      attempts: 1,
    },
    { id: 3, invoiceId: 1003, state: "skipped", code: "invoice_draft", detail: "", attempts: 0 },
    {
      id: 4,
      invoiceId: 1005,
      number: 1004,
      channel: "email",
      state: "uncertain",
      code: "send_uncertain",
      detail: "",
      attempts: 2,
    },
  ],
});

/** The fetch fake for the list and a batch: meta, the list, a batch's POST and its page, and posted. */
const server = (detail: SendBatchDetail = batchDetail()) =>
  stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
    const url = path(input);
    const method = init?.method ?? "GET";
    if (url === "/api/v1/invoices/meta") return jsonResponse(200, meta());
    if (url.startsWith("/api/v1/invoices?")) return jsonResponse(200, listPage());
    if (url === "/api/v1/invoices/send-batches" && method === "POST") return jsonResponse(202, detail);
    if (url === "/api/v1/invoices/send-batches/1001/posted" && method === "POST") return jsonResponse(200, detail);
    if (url === "/api/v1/invoices/send-batches/1001") return jsonResponse(200, detail);
    return new Response(null, { status: 404 });
  });

describe("Send all on the invoice list", () => {
  it("chooses documents, says a draft is skipped, confirms and opens the batch", async () => {
    const fetchMock = server();
    const { router } = renderRoute("/invoices");

    await userEvent.click(await screen.findByRole("checkbox", { name: "Choose document Draft" }));
    await userEvent.click(screen.getByRole("checkbox", { name: "Choose document 1000" }));
    const bar = screen.getByTestId("selection-bar");
    expect(within(bar).getByText("2 selected")).toBeInTheDocument();
    expect(within(bar).getByText("1 draft is not sent: it is listed as skipped.")).toBeInTheDocument();

    await userEvent.click(within(bar).getByRole("button", { name: "Send all" }));
    expect(await screen.findByText("Send 2 documents?")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Send 2 documents" }));

    await waitFor(() => expect(router.state.location.pathname).toBe("/invoices/send-batches/1001"));
    expect(sent(fetchMock, "POST").body).toEqual({ invoiceIds: [1003, 1001] });
  });

  it("chooses the whole page from the header, and clears", async () => {
    server();
    renderRoute("/invoices");

    await userEvent.click(await screen.findByRole("checkbox", { name: "Choose every document on this page" }));
    expect(screen.getByText("3 selected")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Clear" }));
    expect(screen.queryByTestId("selection-bar")).not.toBeInTheDocument();
  });

  it("offers no checkbox without invoices:issue", async () => {
    stubFetch((input: RequestInfo | URL) => {
      const url = path(input);
      if (url === "/api/v1/invoices/meta")
        return jsonResponse(200, meta({ capabilities: { ...meta().capabilities, canIssue: false } }));
      if (url.startsWith("/api/v1/invoices?")) return jsonResponse(200, listPage());
      return new Response(null, { status: 404 });
    });
    renderRoute("/invoices");

    expect(await screen.findByText("1000")).toBeInTheDocument();
    expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
  });
});

describe("a send batch's page", () => {
  it("counts the outcomes, says each in words, and warns of the uncertain", async () => {
    server();
    renderRoute("/invoices/send-batches/1001");

    expect(await screen.findByRole("heading", { name: "Send all 1001" })).toBeInTheDocument();
    const countBadges = await screen.findByTestId("send-batch-counts");
    expect(within(countBadges).getByText("Sent: 1")).toBeInTheDocument();
    expect(within(countBadges).getByText("Awaiting print: 1")).toBeInTheDocument();
    expect(screen.getByText("Some documents may have been sent")).toBeInTheDocument();

    const draftRow = screen.getByTestId("send-job-3");
    expect(within(draftRow).getByText("Skipped")).toBeInTheDocument();
    expect(within(draftRow).getByText("A draft is not sent: issue it first.")).toBeInTheDocument();
    const mailed = screen.getByTestId("send-job-1");
    expect(within(mailed).getByText("E-mail")).toBeInTheDocument();
    expect(within(mailed).getByRole("link", { name: "Invoice 1000" })).toHaveAttribute("href", "/invoices/1001");
    expect(within(screen.getByTestId("send-job-4")).getByText(/^It may have been e-mailed/)).toBeInTheDocument();
  });

  it("shows progress while the batch runs", async () => {
    server(batchDetail({ finishedAt: undefined, counts: counts({ queued: 2, sent: 2 }) }));
    renderRoute("/invoices/send-batches/1001");

    expect(await screen.findByTestId("send-batch-progress")).toBeInTheDocument();
  });

  it("downloads for printing and marks the batch posted", async () => {
    const fetchMock = server();
    renderRoute("/invoices/send-batches/1001");

    expect(await screen.findByRole("button", { name: "Download for printing" })).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Mark as posted" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("button", { name: "Mark as posted" }));

    await waitFor(() => expect(screen.getByText("The documents are marked as posted")).toBeInTheDocument());
    expect(sent(fetchMock, "POST")).toEqual({
      url: "/api/v1/invoices/send-batches/1001/posted",
      body: { postedOn: "2026-09-12" },
    });
  });

  it("offers no print action when nothing awaits print", async () => {
    server(batchDetail({ counts: counts({ sent: 1 }) }));
    renderRoute("/invoices/send-batches/1001");

    expect(await screen.findByRole("heading", { name: "Send all 1001" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Download for printing" })).not.toBeInTheDocument();
  });
});

describe("Send all on a billing run", () => {
  it("is offered on a finished run with issued documents, and lists the run's batches", async () => {
    const fetchMock = billingServer({
      run: billingRun({ status: "done", finishedAt: "2026-09-12T08:02:00Z" }),
      answers: {
        "GET /api/v1/invoices/send-batches": jsonResponse(200, {
          data: [{ ...batchDetail().batch, billingRunId: 7 }],
        }),
        "POST /api/v1/invoices/send-batches": jsonResponse(202, batchDetail()),
      },
    });
    const { router } = renderRoute("/invoices/billing-runs/7");

    expect(await screen.findByTestId("billing-run-sending")).toHaveTextContent("Send all 1001");
    await userEvent.click(screen.getByRole("button", { name: "Send all" }));
    expect(await screen.findByText("Send 1 document?")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Send 1 document" }));

    await waitFor(() => expect(router.state.location.pathname).toBe("/invoices/send-batches/1001"));
    expect(sent(fetchMock, "POST").body).toEqual({ billingRunId: 7 });
  });

  it("is not offered while the run is at work", async () => {
    billingServer();
    renderRoute("/invoices/billing-runs/7");

    expect(await screen.findByRole("button", { name: "Cancel the run" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Send all" })).not.toBeInTheDocument();
  });
});
