import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { jsonResponse, path, refusal } from "../test/api";
import { pendingResponse, readsOf, requestTo, documentServer as server } from "../test/document-server";
import {
  attemptedTransmission,
  draft,
  type EhfStatus,
  ehfDocument,
  ehfState,
  issued,
  partlyPaid,
} from "../test/fixtures";
import { renderRoute } from "../test/route-tree";

/** A caller who may send as EHF on an installation that can: meta's `canSendEhf`. */
const ehfSender = { canSendEhf: true };

const ehfCard = () => screen.findByTestId("ehf-card");

const openEhfDialog = async () => {
  await userEvent.click(await screen.findByRole("button", { name: "Send as EHF" }));
  return screen.findByRole("dialog");
};

describe("the issued document's primary action (EHF and KID design D10)", () => {
  it("is Send as EHF, with e-mail secondary, when the customer prefers EHF and the caller can send it", async () => {
    server(() => ehfDocument("not_sent"), {}, ehfSender);
    renderRoute("/invoices/1001");

    const card = await ehfCard();
    const ehf = screen.getAllByRole("button", { name: "Send as EHF" });
    expect(ehf).toHaveLength(1);
    expect(card).not.toContainElement(ehf[0]);
    expect(ehf[0]).toHaveAttribute("data-variant", "filled");
    expect(screen.getByRole("button", { name: "Send" })).toHaveAttribute("data-variant", "default");
  });

  it("is Send as EHF when the buyer has a Peppol id and no preference is set", async () => {
    server(() => ehfDocument("not_sent", { preference: undefined }), {}, ehfSender);
    renderRoute("/invoices/1001");

    const card = await ehfCard();
    const ehf = screen.getAllByRole("button", { name: "Send as EHF" });
    expect(ehf).toHaveLength(1);
    expect(card).not.toContainElement(ehf[0]);
    expect(ehf[0]).toHaveAttribute("data-variant", "filled");
  });

  it("stays e-mail when the customer prefers e-mail: Send as EHF is offered on the card only", async () => {
    server(() => ehfDocument("not_sent", { preference: "email" }), {}, ehfSender);
    renderRoute("/invoices/1001");

    const card = await ehfCard();
    const ehf = screen.getAllByRole("button", { name: "Send as EHF" });
    expect(ehf).toHaveLength(1);
    expect(card).toContainElement(ehf[0]);
    expect(ehf[0]).not.toHaveAttribute("data-variant", "filled");
  });

  it("offers no EHF send without canSendEhf, even on a document the server says can go", async () => {
    // canSend true and the customer prefers EHF, but meta's canSendEhf is false.
    server(() => ehfDocument("not_sent"));
    renderRoute("/invoices/1001");

    const card = await ehfCard();
    expect(screen.getByRole("button", { name: "Send" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Send as EHF" })).not.toBeInTheDocument();
    expect(within(card).queryByRole("button", { name: "Send as EHF" })).not.toBeInTheDocument();
  });

  it("keeps Download PDF secondary when Send as EHF is the primary action", async () => {
    server(() => ehfDocument("not_sent"), {}, ehfSender);
    renderRoute("/invoices/1001");

    await ehfCard();
    expect(screen.getByRole("button", { name: "Download PDF" })).toHaveAttribute("data-variant", "default");
  });

  it("offers no EHF send at all without canSendEhf, and the card says why", async () => {
    server(() => ehfDocument("not_sent", { canSend: false, blockedBy: "ehf_unavailable" }));
    renderRoute("/invoices/1001");

    const card = await ehfCard();
    expect(screen.getByRole("button", { name: "Send" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Send as EHF" })).not.toBeInTheDocument();
    expect(
      within(card).getByText("This installation cannot send EHF yet: see E-invoicing in the invoice settings."),
    ).toBeInTheDocument();
  });
});

describe("the Send as EHF dialog", () => {
  it("names the receiver and the document, queues it, and says so", async () => {
    const pending = pendingResponse();
    const fetchMock = server(
      () => ehfDocument("not_sent"),
      { "POST /api/v1/invoices/1001/send-ehf": () => pending.response },
      ehfSender,
    );
    renderRoute("/invoices/1001");

    const dialog = await openEhfDialog();
    expect(within(dialog).getByText("To Peppol id 0192:923609016")).toBeInTheDocument();
    expect(within(dialog).getByText(/^Invoice 1000, NOK\s?124\.99$/)).toBeInTheDocument();
    expect(within(dialog).getByText(/the Peppol network is asked whether the receiver accepts/)).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: "Send as EHF" }));
    await waitFor(() => expect(within(dialog).getByRole("button", { name: "Send as EHF" })).toBeDisabled());
    expect(requestTo(fetchMock, "POST", "/api/v1/invoices/1001/send-ehf")).toEqual({});
    const reads = readsOf(fetchMock, "/api/v1/invoices/1001");
    pending.answer(jsonResponse(200, ehfDocument("queued")));
    expect(await screen.findByText("Queued for sending as EHF")).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    await waitFor(() => expect(readsOf(fetchMock, "/api/v1/invoices/1001")).toBeGreaterThan(reads));
  });

  it("notes that the document already went by e-mail", async () => {
    server(() => partlyPaid({ ehf: ehfState("failed") }), {}, ehfSender);
    renderRoute("/invoices/1001");

    const dialog = await openEhfDialog();
    expect(within(dialog).getByText("This document has already been sent by e-mail.")).toBeInTheDocument();
  });

  it.each([
    [
      409,
      "no_peppol_id",
      {},
      "The document was issued to a buyer without a Peppol id, so it cannot be sent as EHF. Send it by e-mail, or credit it and issue it again once the customer has one.",
    ],
    [
      409,
      "buyer_reference_missing",
      {},
      "EHF needs the buyer's reference or an order reference, and the document has neither. Credit it and issue it again with one.",
    ],
    [
      409,
      "ehf_already_sent",
      {},
      "The document is already on its way as EHF, or delivered. Cancel or resolve that transmission first.",
    ],
    [409, "customer_anonymised", {}, "The customer has been anonymised and is not contacted again."],
    [
      502,
      "peppol_lookup_failed",
      {},
      "The Peppol network could not be asked whether the receiver accepts EHF. Try again.",
    ],
    [
      503,
      "ehf_unavailable",
      {},
      "E-invoicing is not available here: the operator has switched it off, the Peppol lookup is off, the access point's credentials are missing or can no longer be read, or the seller's Peppol id is missing.",
    ],
    [
      503,
      "storage_unavailable",
      {},
      "The document store is unavailable, so nothing can be issued, downloaded or sent now.",
    ],
  ] as const)("says a %i %s in words, never the server's English", async (status, code, extra, words) => {
    server(
      () => ehfDocument("not_sent"),
      { "POST /api/v1/invoices/1001/send-ehf": () => refusal(status, code, extra) },
      ehfSender,
    );
    renderRoute("/invoices/1001");

    const dialog = await openEhfDialog();
    await userEvent.click(within(dialog).getByRole("button", { name: "Send as EHF" }));
    expect(await within(dialog).findByText(words)).toBeInTheDocument();
    expect(screen.queryByText("The server's English.")).not.toBeInTheDocument();
  });

  it("says the rate limit in words", async () => {
    server(
      () => ehfDocument("not_sent"),
      { "POST /api/v1/invoices/1001/send-ehf": () => jsonResponse(429, { error: { code: "rate_limited" } }) },
      ehfSender,
    );
    renderRoute("/invoices/1001");

    const dialog = await openEhfDialog();
    await userEvent.click(within(dialog).getByRole("button", { name: "Send as EHF" }));
    expect(
      await within(dialog).findByText("Too many requests in a short time; wait ten minutes and try again."),
    ).toBeInTheDocument();
  });

  it.each([
    [{ peppolRegistered: false, peppolCanReceive: false }, "The receiver is not registered on the Peppol network."],
    [
      { peppolRegistered: true, peppolCanReceive: false },
      "The receiver is registered on the Peppol network, but does not accept this kind of document.",
    ],
  ])("says what the network answered when the receiver cannot take it (%o)", async (extra, words) => {
    server(
      () => ehfDocument("not_sent"),
      { "POST /api/v1/invoices/1001/send-ehf": () => refusal(409, "peppol_not_receivable", extra) },
      ehfSender,
    );
    renderRoute("/invoices/1001");

    const dialog = await openEhfDialog();
    await userEvent.click(within(dialog).getByRole("button", { name: "Send as EHF" }));
    expect(
      await within(dialog).findByText(
        "The receiver does not accept this document as EHF on the Peppol network. Send it by e-mail instead.",
      ),
    ).toBeInTheDocument();
    expect(within(dialog).getByText(words)).toBeInTheDocument();
  });

  it("names every rule an invalid EHF breaks", async () => {
    server(
      () => ehfDocument("not_sent"),
      {
        "POST /api/v1/invoices/1001/send-ehf": () =>
          refusal(409, "ehf_invalid", {
            rules: [
              { id: "PEPPOL-EN16931-R003", message: "A buyer reference or purchase order reference MUST be provided." },
              { id: "vat_category_k_unsupported", message: "VAT category K is not supported." },
            ],
          }),
      },
      ehfSender,
    );
    renderRoute("/invoices/1001");

    const dialog = await openEhfDialog();
    await userEvent.click(within(dialog).getByRole("button", { name: "Send as EHF" }));
    expect(
      await within(dialog).findByText("The document's EHF breaks a Peppol rule, so it cannot be sent as EHF."),
    ).toBeInTheDocument();
    expect(within(dialog).getByText("PEPPOL-EN16931-R003")).toBeInTheDocument();
    expect(
      within(dialog).getByText("A line is in VAT category K (intra-community supply), which is not sent as EHF."),
    ).toBeInTheDocument();
  });
});

describe("the E-invoice card", () => {
  it.each([
    ["not_sent", "Not sent"],
    ["queued", "Queued"],
    ["submitted", "Submitted"],
    ["delivered", "Delivered to the receiver's access point"],
    ["failed", "Failed"],
    ["unconfirmed", "Unconfirmed – needs a check with the provider"],
    ["cancelled", "Cancelled"],
  ] as const)("says %s as %s", async (status: EhfStatus, words) => {
    server(() => ehfDocument(status), {}, ehfSender);
    renderRoute("/invoices/1001");

    const card = await ehfCard();
    const badge = within(card).getByTestId("ehf-status");
    expect(badge).toHaveTextContent(words);
    expect(badge).toHaveAttribute("data-status", status);
  });

  it("shows the latest transmission's timestamps, the provider reference and the reason to an issuer", async () => {
    server(
      () =>
        ehfDocument("failed", {
          submittedAt: "2026-09-12T11:00:05Z",
          providerRef: "8d4e2f1a-3b5c-4d6e-9f0a-1b2c3d4e5f6a",
        }),
      {},
      ehfSender,
    );
    renderRoute("/invoices/1001");

    const card = await ehfCard();
    expect(within(card).getByText(/^Queued Sep 12, 2026/)).toBeInTheDocument();
    expect(within(card).getByText(/^Submitted Sep 12, 2026/)).toBeInTheDocument();
    expect(within(card).getByText(/^Failed Sep 12, 2026/)).toBeInTheDocument();
    expect(within(card).getByText("Provider reference: 8d4e2f1a-3b5c-4d6e-9f0a-1b2c3d4e5f6a")).toBeInTheDocument();
    expect(within(card).getByText("Reason: Storecove: the receiver rejected the document")).toBeInTheDocument();
  });

  it("lists every transmission, newest first, with its receiver", async () => {
    const older = ehfState("failed").transmissions[0];
    server(
      () =>
        ehfDocument("delivered", {
          transmissions: [
            {
              ...ehfState("delivered").transmissions[0],
              id: 1102,
              ublUrl: "/api/v1/invoices/1001/transmissions/1102/ubl",
            },
            older,
          ],
        }),
      {},
      ehfSender,
    );
    renderRoute("/invoices/1001");

    const card = await ehfCard();
    const rows = within(card).getAllByTestId("transmission");
    expect(rows).toHaveLength(2);
    expect(rows[0]).toHaveAttribute("data-transmission", "1102");
    expect(within(rows[0]).getByText("Delivered")).toBeInTheDocument();
    expect(within(rows[1]).getByText("Failed")).toBeInTheDocument();
    expect(within(rows[1]).getByText("0192:923609016")).toBeInTheDocument();
  });

  it("says a document never sent as EHF has no transmissions", async () => {
    server(() => ehfDocument("not_sent"), {}, ehfSender);
    renderRoute("/invoices/1001");

    expect(within(await ehfCard()).getByText("Not sent as EHF yet.")).toBeInTheDocument();
  });

  it("cancels a queued transmission and reads the document again", async () => {
    const fetchMock = server(
      () => ehfDocument("queued"),
      { "POST /api/v1/invoices/1001/transmissions/1101/cancel": () => jsonResponse(200, ehfDocument("cancelled")) },
      ehfSender,
    );
    renderRoute("/invoices/1001");

    const card = await ehfCard();
    const reads = readsOf(fetchMock, "/api/v1/invoices/1001");
    await userEvent.click(within(card).getByRole("button", { name: "Cancel the transmission" }));
    expect(await screen.findByText("The transmission is cancelled")).toBeInTheDocument();
    expect(
      fetchMock.actualCalls.some(
        ([url, init]) => path(url) === "/api/v1/invoices/1001/transmissions/1101/cancel" && init?.method === "POST",
      ),
    ).toBe(true);
    await waitFor(() => expect(readsOf(fetchMock, "/api/v1/invoices/1001")).toBeGreaterThan(reads));
  });

  it("says a cancel the server refuses in words", async () => {
    server(
      () => ehfDocument("queued"),
      { "POST /api/v1/invoices/1001/transmissions/1101/cancel": () => refusal(409, "transmission_not_cancellable") },
      ehfSender,
    );
    renderRoute("/invoices/1001");

    await userEvent.click(within(await ehfCard()).getByRole("button", { name: "Cancel the transmission" }));
    expect(
      await screen.findByText(
        "The transmission may already have reached the access point, so it can no longer be cancelled.",
      ),
    ).toBeInTheDocument();
  });

  it("offers no cancel on a queued transmission already attempted", async () => {
    server(() => ehfDocument("queued", { transmissions: [attemptedTransmission()] }), {}, ehfSender);
    renderRoute("/invoices/1001");

    const card = await ehfCard();
    expect(within(card).getByTestId("transmission")).toBeInTheDocument();
    expect(within(card).queryByRole("button", { name: "Cancel the transmission" })).not.toBeInTheDocument();
  });

  it.each(["submitted", "delivered", "failed", "unconfirmed", "cancelled"] as const)(
    "offers no cancel on a %s transmission",
    async (status) => {
      server(() => ehfDocument(status), {}, ehfSender);
      renderRoute("/invoices/1001");

      await ehfCard();
      expect(screen.queryByRole("button", { name: "Cancel the transmission" })).not.toBeInTheDocument();
    },
  );

  it("resolves an unconfirmed transmission with an outcome and a required note", async () => {
    const fetchMock = server(
      () => ehfDocument("unconfirmed"),
      {
        "POST /api/v1/invoices/1001/transmissions/1101/resolve": () =>
          jsonResponse(200, ehfDocument("failed", { canSend: true })),
      },
      ehfSender,
    );
    renderRoute("/invoices/1001");

    const card = await ehfCard();
    expect(within(card).queryByRole("button", { name: "Cancel the transmission" })).not.toBeInTheDocument();
    await userEvent.click(within(card).getByRole("button", { name: "Resolve" }));
    const dialog = await screen.findByRole("dialog");
    const resolve = within(dialog).getByRole("button", { name: "Resolve" });
    expect(resolve).toBeDisabled();
    await userEvent.click(within(dialog).getByRole("radio", { name: "Failed" }));
    expect(resolve).toBeDisabled();
    await userEvent.type(within(dialog).getByRole("textbox", { name: "What the provider said" }), "Never arrived");
    await userEvent.click(resolve);
    expect(await screen.findByText("The transmission is resolved")).toBeInTheDocument();
    expect(requestTo(fetchMock, "POST", "/api/v1/invoices/1001/transmissions/1101/resolve")).toEqual({
      outcome: "failed",
      note: "Never arrived",
    });
  });

  it.each(["not_sent", "queued", "submitted", "delivered", "failed", "cancelled"] as const)(
    "offers no resolve on a %s document",
    async (status) => {
      server(() => ehfDocument(status), {}, ehfSender);
      renderRoute("/invoices/1001");

      await ehfCard();
      expect(screen.queryByRole("button", { name: "Resolve" })).not.toBeInTheDocument();
    },
  );

  it("downloads each transmission's stored UBL", async () => {
    const createObjectURL = vi.fn(() => "blob:ubl");
    vi.stubGlobal("URL", Object.assign(URL, { createObjectURL, revokeObjectURL: vi.fn() }));
    const clicked: HTMLAnchorElement[] = [];
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (this: HTMLAnchorElement) {
      clicked.push(this);
    });
    const fetchMock = server(
      () => ehfDocument("delivered"),
      {
        "GET /api/v1/invoices/1001/transmissions/1101/ubl": () =>
          new Response("<Invoice/>", {
            status: 200,
            headers: {
              "Content-Type": "application/xml",
              "Content-Disposition": 'attachment; filename="faktura-1000.xml"',
            },
          }),
      },
      ehfSender,
    );
    renderRoute("/invoices/1001");

    await userEvent.click(
      within(await ehfCard()).getByRole("button", { name: /^Download the EHF \(XML\) queued Sep 12, 2026/ }),
    );
    await waitFor(() => expect(clicked).toHaveLength(1));
    expect(fetchMock.actualCalls.some(([url]) => path(url) === "/api/v1/invoices/1001/transmissions/1101/ubl")).toBe(
      true,
    );
    expect(clicked[0].download).toBe("faktura-1000.xml");
  });

  it("says a download the server refuses in words", async () => {
    server(
      () => ehfDocument("delivered"),
      { "GET /api/v1/invoices/1001/transmissions/1101/ubl": () => refusal(503, "storage_unavailable") },
      ehfSender,
    );
    renderRoute("/invoices/1001");

    await userEvent.click(
      within(await ehfCard()).getByRole("button", { name: /^Download the EHF \(XML\) queued Sep 12, 2026/ }),
    );
    expect(await screen.findByText("Could not download the EHF")).toBeInTheDocument();
  });

  it("offers a new send after a failure, on the card when e-mail is preferred", async () => {
    server(() => ehfDocument("failed", { preference: "email" }), {}, ehfSender);
    renderRoute("/invoices/1001");

    const card = await ehfCard();
    expect(within(card).getByRole("button", { name: "Send as EHF" })).toBeInTheDocument();
  });

  it.each([
    [
      "no_peppol_id",
      "The document was issued to a buyer without a Peppol id, so it cannot be sent as EHF. Send it by e-mail, or credit it and issue it again once the customer has one.",
    ],
    [
      "buyer_reference_missing",
      "EHF needs the buyer's reference or an order reference, and the document has neither. Credit it and issue it again with one.",
    ],
    ["ehf_invalid", "A line is in VAT category K (intra-community supply), which is not sent as EHF."],
  ])("says why it cannot be sent: %s", async (blockedBy, words) => {
    server(() => ehfDocument("not_sent", { canSend: false, blockedBy }), {}, ehfSender);
    renderRoute("/invoices/1001");

    const card = await ehfCard();
    expect(within(card).getByText(words)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Send as EHF" })).not.toBeInTheDocument();
  });
});

describe("the draft editor's EHF warning", () => {
  it("says the missing reference beside the references, not in the list above", async () => {
    server(() => draft({ yourReference: "", orderReference: "", warnings: ["ehf_buyer_reference_missing"] }));
    renderRoute("/invoices/1001");

    const words =
      "This customer is invoiced by EHF, which needs your customer's reference or an order reference. Add one before issuing: neither can change afterwards.";
    const warning = await screen.findByText(words);
    expect(warning.closest("[data-testid='ehf-reference-warning']")).not.toBeNull();
    expect(screen.getAllByText(words)).toHaveLength(1);
  });
});

describe("the e-mail dialog beside EHF", () => {
  const openSendDialog = async () => {
    await userEvent.click(await screen.findByRole("button", { name: "Send" }));
    return screen.findByRole("dialog");
  };

  it("says ehf_preferred loud", async () => {
    server(
      () =>
        ehfDocument("not_sent", {}, { sendDefaults: { recipient: "faktura@acme.no", warnings: ["ehf_preferred"] } }),
      {},
      ehfSender,
    );
    renderRoute("/invoices/1001");

    const dialog = await openSendDialog();
    const said = within(dialog).getByText(
      "This customer expects EHF, and this document can be sent as EHF. An e-mailed PDF does not meet the e-invoicing duty.",
    );
    const alert = said.closest("[data-send-warning]") as HTMLElement;
    expect(alert).toHaveAttribute("data-loud", "true");
    expect(within(alert).getByText("The customer expects EHF")).toBeInTheDocument();
  });

  it.each(["queued", "submitted", "delivered", "unconfirmed"] as const)(
    "notes a %s EHF transmission",
    async (status) => {
      server(() => ehfDocument(status));
      renderRoute("/invoices/1001");

      const dialog = await openSendDialog();
      expect(within(dialog).getByText("This document is already on its way as EHF, or delivered.")).toBeInTheDocument();
    },
  );

  it.each(["not_sent", "failed", "cancelled"] as const)("notes nothing after a %s one", async (status) => {
    server(() => ehfDocument(status));
    renderRoute("/invoices/1001");

    const dialog = await openSendDialog();
    expect(within(dialog).queryByText("This document is already on its way as EHF, or delivered.")).toBeNull();
  });

  it("keeps the e-mail send as it was for a document without an EHF block", async () => {
    server(() => issued({ ehf: undefined }));
    renderRoute("/invoices/1001");

    const dialog = await openSendDialog();
    expect(within(dialog).getByRole("textbox", { name: "Recipient" })).toHaveValue("faktura@acme.no");
  });
});
