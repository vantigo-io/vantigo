import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { meta, stubAccountingApi } from "../test/server";

/** The journal's file input. */
const fileInput = () => {
  const input = document.querySelector<HTMLInputElement>('input[type="file"]');
  if (!input) throw new Error("no file input");
  return input;
};

describe("the journal entry", () => {
  it("copies a voucher's lines without its VAT line and posts them", async () => {
    const fetchMock = stubAccountingApi();
    const { router } = renderRoute("/accounting/journal?copy=5001");
    expect(await screen.findByText(/Copied from 2026-000007/)).toBeInTheDocument();
    expect(screen.getByLabelText(/^Description/)).toHaveValue("Husleie oktober");
    const debits = screen.getAllByLabelText("Debit");
    expect(debits[0]).toHaveValue("1000.00");
    const credits = screen.getAllByLabelText("Credit");
    expect(credits[1]).toHaveValue("1250.00");
    await waitFor(() => expect(sent(fetchMock, "POST", /\/vouchers\/preview$/).length).toBeGreaterThan(0));
    expect(await screen.findByText(/Balances/)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Post" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/vouchers$/)).toHaveLength(1));
    const body = sent(fetchMock, "POST", /\/vouchers$/)[0].body as {
      lines: { amount: string; taxCodeId: number | null }[];
    };
    expect(body.lines.map((l) => [l.amount, l.taxCodeId])).toEqual([
      ["1000.00", 2001],
      ["-1250.00", null],
    ]);
    await waitFor(() => expect(router.state.location.pathname).toBe("/accounting/vouchers/5002"));
  });

  it("goes to the posted voucher and says so when a file fails after the post", async () => {
    const fetchMock = stubAccountingApi({ attachmentFails: true });
    const { router } = renderRoute("/accounting/journal?copy=5001");
    expect(await screen.findByText(/Copied from 2026-000007/)).toBeInTheDocument();
    await userEvent.upload(fileInput(), new File(["%PDF"], "receipt.pdf", { type: "application/pdf" }));
    expect(await screen.findByText(/Balances/)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Post" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/accounting/vouchers/5002"));
    expect(sent(fetchMock, "POST", /\/vouchers$/)).toHaveLength(1);
    expect(await screen.findByText(/Posted as 2026-000008, but receipt.pdf could not be attached/)).toBeInTheDocument();
  });

  it("checks the files before posting: storage and size", async () => {
    stubAccountingApi({ meta: meta({ attachmentsEnabled: false }) });
    renderRoute("/accounting/journal?copy=5001");
    expect(await screen.findByText(/Copied from 2026-000007/)).toBeInTheDocument();
    await userEvent.upload(fileInput(), new File(["%PDF"], "receipt.pdf", { type: "application/pdf" }));
    expect(await screen.findByText(/has no file storage/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Post" })).toBeDisabled();
  });

  it("tells someone who may not post", async () => {
    stubAccountingApi({ meta: meta({ canPost: false }) });
    renderRoute("/accounting/journal");
    expect(await screen.findByText(/You cannot post vouchers/)).toBeInTheDocument();
  });
});
