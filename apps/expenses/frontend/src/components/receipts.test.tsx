import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { attachment } from "../test/fixtures";
import { renderWithProviders } from "../test/render";
import { ReceiptThumbnails } from "./receipt-thumbnails";

const photos = [
  attachment({ id: 1, fileName: "first.jpg", contentType: "image/jpeg" }),
  attachment({ id: 2, fileName: "second.png", contentType: "image/png" }),
];

describe("ReceiptThumbnails", () => {
  it("draws an image receipt inline, named after its file", () => {
    renderWithProviders(<ReceiptThumbnails attachments={photos} />);

    const first = screen.getByRole("img", { name: "first.jpg" });
    expect(first).toHaveAttribute("src", "/api/v1/expenses/attachments/1");
    expect(screen.getByRole("img", { name: "second.png" })).toBeInTheDocument();
  });

  it("opens a receipt in a viewer the keyboard can walk through, and closes on Escape", async () => {
    renderWithProviders(<ReceiptThumbnails attachments={photos} />);

    await userEvent.click(screen.getByRole("button", { name: "View first.jpg" }));
    const viewer = await screen.findByRole("dialog", { name: "first.jpg" });
    expect(within(viewer).getByRole("img", { name: "first.jpg" })).toBeInTheDocument();
    expect(viewer).toHaveTextContent("1 of 2");

    await userEvent.keyboard("{ArrowRight}");
    expect(await screen.findByRole("dialog", { name: "second.png" })).toHaveTextContent("2 of 2");

    await userEvent.keyboard("{ArrowLeft}");
    expect(await screen.findByRole("dialog", { name: "first.jpg" })).toBeInTheDocument();

    await userEvent.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("gives a PDF a link that opens in a new tab, with its name and size, and never a frame", () => {
    const { container } = renderWithProviders(
      <ReceiptThumbnails
        attachments={[
          attachment({ id: 7, fileName: "invoice.pdf", contentType: "application/pdf", sizeBytes: 2 * 1024 * 1024 }),
        ]}
      />,
    );

    const link = screen.getByRole("link", { name: "Open invoice.pdf in a new tab" });
    expect(link).toHaveAttribute("href", "/api/v1/expenses/attachments/7");
    expect(link).toHaveAttribute("target", "_blank");
    expect(link).toHaveAttribute("rel", expect.stringContaining("noopener"));
    expect(link).toHaveTextContent("invoice.pdf");
    expect(link).toHaveTextContent("2 MB");
    // The platform sends X-Frame-Options: DENY and object-src 'none', and the
    // receipt carries its own default-src 'none'; sandbox — a framed receipt
    // would render as an empty box.
    expect(container.querySelector("iframe, object, embed")).toBeNull();
  });

  it("gives a HEIC the same link rather than an image only Safari would draw", () => {
    renderWithProviders(
      <ReceiptThumbnails attachments={[attachment({ id: 8, fileName: "photo.heic", contentType: "image/heic" })]} />,
    );

    expect(screen.getByRole("link", { name: "Open photo.heic in a new tab" })).toBeInTheDocument();
    expect(screen.queryByRole("img", { name: "photo.heic" })).not.toBeInTheDocument();
  });

  it("offers a remove button named after its own receipt when the expense allows it", async () => {
    const removed: number[] = [];
    renderWithProviders(<ReceiptThumbnails attachments={photos} onRemove={(id) => removed.push(id)} />);

    await userEvent.click(screen.getByRole("button", { name: "Remove second.png" }));
    expect(removed).toEqual([2]);
  });
});
