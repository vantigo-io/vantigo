import { notifyManager } from "@tanstack/react-query";
import { act, fireEvent, screen } from "@testing-library/react";
import { useState } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { OnSitePaymentStatus } from "../api/attempts";
import { jsonResponse, path } from "../test/api";
import { stubFetch } from "../test/fetch";
import { onSitePayment } from "../test/fixtures";
import { renderWithProviders } from "../test/render";
import { QR_POLL_MS, QrScreen } from "./-qr-screen";

const STATUS = "/api/v1/invoices/1001/payment-requests/1001";

/** The status read's fake, answering the current `state`; counts the reads. */
const server = (state: () => OnSitePaymentStatus) =>
  stubFetch((input: RequestInfo | URL) =>
    path(input) === STATUS ? jsonResponse(200, state()) : new Response(null, { status: 404 }),
  );
const reads = (fetchMock: ReturnType<typeof server>) =>
  fetchMock.actualCalls.filter(([url]) => path(url) === STATUS).length;

/** The real `setImmediate`, which the fake clock leaves alone: a turn of real I/O. */
const realTurn = () =>
  new Promise<void>((resolve) =>
    (globalThis as unknown as { setImmediate: (callback: () => void) => void }).setImmediate(resolve),
  );

/**
 * Lets the fake clock run `ms`, then lets what it released settle — the
 * response's body, read on real I/O, and the query's notifications, on
 * zero-delay timers — without moving the clock any further.
 */
const elapse = (ms: number) =>
  act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
    for (let round = 0; round < 10; round++) {
      await realTurn();
      await vi.advanceTimersByTimeAsync(0);
    }
  });

/** The QR screen inside a stand-in for its modal, which the test can close as a person would. */
const InModal = () => {
  const [open, setOpen] = useState(true);
  return (
    <>
      <button type="button" onClick={() => setOpen(false)}>
        Close the modal
      </button>
      {open && <QrScreen invoiceId={1001} attemptId={1001} qrSvg={onSitePayment().qrSvg} onClose={() => {}} />}
    </>
  );
};

describe("QrScreen's poll", () => {
  beforeEach(() => {
    // The query's notifications go out at once rather than on a zero-delay
    // timer, so what a read answered is on screen when the clock stops.
    notifyManager.setScheduler((callback) => callback());
    // Only the clock the poll runs on is faked; setImmediate stays real, for the I/O.
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "setInterval", "clearInterval"] });
  });
  afterEach(() => {
    vi.useRealTimers();
    notifyManager.setScheduler((callback) => setTimeout(callback, 0));
  });

  it("reads the request every two seconds and stops once it is captured", async () => {
    let state: OnSitePaymentStatus = { state: "created", qrSvg: onSitePayment().qrSvg };
    const fetchMock = server(() => state);
    renderWithProviders(<InModal />);

    await elapse(0);
    expect(reads(fetchMock)).toBe(1);
    await elapse(QR_POLL_MS - 1);
    expect(reads(fetchMock)).toBe(1);
    await elapse(1);
    expect(reads(fetchMock)).toBe(2);
    expect(QR_POLL_MS).toBe(2000);

    state = { state: "captured", capturedAt: "2026-09-12T10:32:00Z" };
    await elapse(QR_POLL_MS);
    expect(reads(fetchMock)).toBe(3);
    expect(screen.getByText("Paid. The payment is registered on the invoice.")).toBeInTheDocument();
    await elapse(QR_POLL_MS * 10);
    expect(reads(fetchMock)).toBe(3);
  });

  it("reads nothing more once the modal is closed", async () => {
    const fetchMock = server(() => ({ state: "created", qrSvg: onSitePayment().qrSvg }));
    renderWithProviders(<InModal />);

    await elapse(QR_POLL_MS);
    expect(reads(fetchMock)).toBe(2);
    fireEvent.click(screen.getByRole("button", { name: "Close the modal" }));
    await elapse(QR_POLL_MS * 10);
    expect(reads(fetchMock)).toBe(2);
  });
});
