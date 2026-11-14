import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { meta, stubAccountingApi, voucher } from "../test/server";

describe("a voucher", () => {
  it("shows its postings and reverses it with a reason", async () => {
    const fetchMock = stubAccountingApi();
    renderRoute("/accounting/vouchers/5001");
    expect(await screen.findByText("Voucher 2026-000007")).toBeInTheDocument();
    expect(screen.getByText("6300 Leie av lokaler")).toBeInTheDocument();
    expect(screen.getByText("VAT")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Reverse" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.type(within(dialog).getByLabelText(/Reason/), "Wrong month");
    await userEvent.click(within(dialog).getByRole("button", { name: "Reverse" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/reverse$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/reverse$/)[0].body).toEqual({ date: "2026-11-14", reason: "Wrong month" });
  });

  it("offers no Reverse on a voucher the system posted", async () => {
    stubAccountingApi({
      vouchers: [
        voucher({
          system: true,
          kind: "sales_invoice",
          source: { module: "invoices", kind: "document", id: 9, part: "" },
        }),
      ],
    });
    renderRoute("/accounting/vouchers/5001");
    expect(await screen.findByText("Posted by the system")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Reverse" })).not.toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Copy" })).not.toBeInTheDocument();
  });

  it("offers Reverse on the opening only to who may manage the books", async () => {
    stubAccountingApi({
      meta: meta({ canClose: false }),
      vouchers: [voucher({ kind: "opening", description: "Inngående balanse" })],
    });
    renderRoute("/accounting/vouchers/5001");
    expect(await screen.findByText("Voucher 2026-000007")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Reverse" })).not.toBeInTheDocument();
  });
});
