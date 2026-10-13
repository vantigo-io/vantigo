import { notifyManager } from "@tanstack/react-query";
import { act, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { setLanguagePreference } from "@vantigo/frontend-shell";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { type PayAttemptStatus, payNavigation } from "../api/pay";
import { invoicesCatalog } from "../i18n";
import { jsonResponse, path, refusal } from "../test/api";
import { stubFetch } from "../test/fetch";
import { payPage } from "../test/fixtures";
import { renderWithProviders } from "../test/render";
import {
  PAY_POLL_FAST_MS,
  PAY_POLL_FAST_READS,
  PAY_POLL_MAX_READS,
  PAY_POLL_SLOW_MS,
  PAY_STORAGE_KEY,
  PayPage,
} from "./pay";

const TOKEN = "tok-Secret_123";
const READ = "/api/v1/invoices/pay/read";
const ATTEMPTS = "/api/v1/invoices/pay/attempts";
const REFERENCE = "k3x9q2-10042-77";
const NONCE = "nonce-abc";
const STATUS = `${ATTEMPTS}/${REFERENCE}?nonce=${NONCE}`;
const VIPPS = "https://apitest.vipps.no/dwo-api-application/v1/deeplink/vippsgateway?v=2&token=eyJvipps&x=1";

const en = invoicesCatalog.en;
const nb = invoicesCatalog.nb;
type Key = keyof typeof en;

interface PayServer {
  read?: () => Response;
  attempt?: () => Response;
  status?: () => PayAttemptStatus | Response;
}

/** The three anonymous operations' fake; anything else is a 404 the page must never ask for. */
const server = ({ read, attempt, status }: PayServer = {}) =>
  stubFetch(
    (input: RequestInfo | URL, init?: RequestInit) => {
      const url = path(input);
      const method = init?.method ?? "GET";
      if (url === READ && method === "POST") return read ? read() : jsonResponse(200, payPage({ language: "en" }));
      if (url === ATTEMPTS && method === "POST")
        return attempt ? attempt() : jsonResponse(201, { reference: REFERENCE, redirectUrl: VIPPS });
      if (url === STATUS && method === "GET") {
        const answer = status ? status() : { state: "pending" };
        return answer instanceof Response ? answer : jsonResponse(200, answer);
      }
      return new Response(null, { status: 404 });
    },
    { session: "delegate" },
  );

const at = (address: string) => window.history.replaceState(null, "", address);

const stored = () => JSON.parse(window.sessionStorage.getItem(PAY_STORAGE_KEY) ?? "{}") as Record<string, string>;

beforeEach(() => {
  window.sessionStorage.clear();
  vi.spyOn(payNavigation, "open").mockImplementation(() => {});
});

afterEach(() => {
  at("/");
  window.sessionStorage.clear();
  setLanguagePreference("auto");
});

describe("PayPage", () => {
  it("shows the invoice and the KID, never the buyer, and reads with the token in the body", async () => {
    at(`/pay?token=${TOKEN}`);
    // Whatever else an answer carried, the page shows only what it knows to show.
    const fetchMock = server({
      read: () => jsonResponse(200, { ...payPage({ language: "en" }), buyer: { name: "Kari Kunde" } }),
    });
    renderWithProviders(<PayPage />);

    expect(await screen.findByText("Vantigo Demo AS")).toBeInTheDocument();
    expect(screen.getByText("999888777")).toBeInTheDocument();
    expect(screen.getByText("10042")).toBeInTheDocument();
    expect(screen.getByText("0001004207")).toBeInTheDocument();
    expect(screen.getByTestId("pay-open-amount")).toHaveTextContent(/1,250\.00/);
    expect(screen.getByTestId("pay-due-date")).toHaveTextContent("Sep 15, 2026");
    expect(screen.queryByText(/Kari Kunde/)).not.toBeInTheDocument();
    expect(screen.getByRole("link", { name: en["pay.terms"] })).toHaveAttribute(
      "href",
      "https://seller.example.com/terms",
    );
    expect(screen.getByRole("link", { name: en["pay.privacy"] })).toHaveAttribute(
      "href",
      "https://seller.example.com/privacy",
    );

    const [, init] = fetchMock.actualCalls.find(([u]) => path(u) === READ) ?? [];
    expect(init?.method).toBe("POST");
    expect(JSON.parse(String(init?.body))).toEqual({ token: TOKEN });
    // Read once, kept for the tab, and gone from the address bar at once.
    expect(window.location.search).toBe("");
    expect(window.location.pathname).toBe("/pay");
    expect(stored().token).toBe(TOKEN);
  });

  it("speaks the buyer's language from the read, and the reader may choose the other", async () => {
    at(`/pay?token=${TOKEN}`);
    server({ read: () => jsonResponse(200, payPage({ language: "nb" })) });
    renderWithProviders(<PayPage />);

    expect(await screen.findByRole("button", { name: nb["pay.payWithVipps"] })).toBeInTheDocument();
    await userEvent.click(screen.getByRole("radio", { name: en["pay.language.en"] }));
    expect(await screen.findByRole("button", { name: en["pay.payWithVipps"] })).toBeInTheDocument();
  });

  it("works when the browser refuses storage", async () => {
    at(`/pay?token=${TOKEN}`);
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("SecurityError");
    });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("SecurityError");
    });
    const fetchMock = server();
    renderWithProviders(<PayPage />);

    expect(await screen.findByText("Vantigo Demo AS")).toBeInTheDocument();
    expect(window.location.search).toBe("");
    const [, init] = fetchMock.actualCalls.find(([u]) => path(u) === READ) ?? [];
    expect(JSON.parse(String(init?.body))).toEqual({ token: TOKEN });
  });

  it("requires the terms to be accepted before anything is sent", async () => {
    at(`/pay?token=${TOKEN}`);
    const fetchMock = server();
    renderWithProviders(<PayPage />);

    await userEvent.click(await screen.findByRole("button", { name: en["pay.payWithVipps"] }));
    expect(await screen.findByText(en["pay.acceptRequired"])).toBeInTheDocument();
    expect(fetchMock.actualCalls.some(([u]) => path(u) === ATTEMPTS)).toBe(false);

    await userEvent.click(screen.getByRole("checkbox", { name: en["pay.accept"] }));
    await userEvent.click(screen.getByRole("button", { name: en["pay.payWithVipps"] }));
    await waitFor(() => expect(payNavigation.open).toHaveBeenCalled());
    const [, init] = fetchMock.actualCalls.find(([u]) => path(u) === ATTEMPTS) ?? [];
    expect(init?.method).toBe("POST");
    expect(JSON.parse(String(init?.body))).toEqual({ token: TOKEN, acceptTerms: true });
  });

  it("opens Vipps' address at once and unchanged", async () => {
    at(`/pay?token=${TOKEN}`);
    server();
    renderWithProviders(<PayPage />);

    await userEvent.click(await screen.findByRole("checkbox", { name: en["pay.accept"] }));
    await userEvent.click(screen.getByRole("button", { name: en["pay.payWithVipps"] }));
    await waitFor(() => expect(payNavigation.open).toHaveBeenCalledTimes(1));
    expect(payNavigation.open).toHaveBeenCalledWith(VIPPS);
  });

  it("says that an address with no token names no invoice, and asks for nothing", async () => {
    at("/pay");
    const fetchMock = server();
    renderWithProviders(<PayPage />);

    expect(await screen.findByText(en["pay.noLink"])).toBeInTheDocument();
    expect(fetchMock.actualCalls).toHaveLength(0);
  });

  it("says an unknown token in words", async () => {
    at(`/pay?token=${TOKEN}`);
    server({ read: () => new Response(null, { status: 404 }) });
    renderWithProviders(<PayPage />);

    expect(await screen.findByText(en["pay.refusal.notFound"])).toBeInTheDocument();
  });

  const reasons = [
    "revoked",
    "credited",
    "settled",
    "handed_off",
    "unavailable",
    "payment_in_progress",
    "amount_out_of_range",
  ] as const;

  for (const language of ["en", "nb"] as const) {
    const words = invoicesCatalog[language] as Record<Key, string>;
    for (const reason of reasons) {
      it(`says the read's ${reason} in words (${language}), offering no payment`, async () => {
        at(`/pay?token=${TOKEN}`);
        server({ read: () => jsonResponse(200, payPage({ language, payable: false, reason })) });
        renderWithProviders(<PayPage />);

        expect(await screen.findByText(words[`pay.reason.${reason}` as Key])).toBeInTheDocument();
        // The KID stays, so the payer can pay by bank.
        expect(screen.getByText("0001004207")).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: words["pay.payWithVipps"] })).not.toBeInTheDocument();
      });
    }

    const refusals: [string, () => Response, Key][] = [
      ["a 409 with its reason", () => refusal(409, "invoice_settled", { reason: "settled" }), "pay.reason.settled"],
      [
        "a 409 payment_in_progress",
        () => refusal(409, "payment_in_progress", { reason: "payment_in_progress" }),
        "pay.reason.payment_in_progress",
      ],
      ["too_many_attempts", () => refusal(409, "too_many_attempts"), "pay.refusal.too_many_attempts"],
      ["a 409 payments_unavailable", () => refusal(409, "payments_unavailable"), "pay.reason.unavailable"],
      ["a 503 payments_unavailable", () => refusal(503, "payments_unavailable"), "pay.reason.unavailable"],
      ["a 502 provider_failed", () => refusal(502, "provider_failed"), "pay.refusal.provider_failed"],
      [
        "a 429",
        () => jsonResponse(429, { error: { code: "rate_limited", message: "Too many requests" } }),
        "pay.refusal.rateLimited",
      ],
      ["a 404", () => new Response(null, { status: 404 }), "pay.refusal.notFound"],
    ];
    for (const [name, answer, key] of refusals) {
      it(`says the attempt's ${name} in words (${language}), never the server's English`, async () => {
        at(`/pay?token=${TOKEN}`);
        server({ read: () => jsonResponse(200, payPage({ language })), attempt: answer });
        renderWithProviders(<PayPage />);

        await userEvent.click(await screen.findByRole("checkbox", { name: words["pay.accept"] }));
        await userEvent.click(screen.getByRole("button", { name: words["pay.payWithVipps"] }));
        expect(await screen.findByText(words[key])).toBeInTheDocument();
        expect(screen.queryByText("The server's English.")).not.toBeInTheDocument();
        expect(payNavigation.open).not.toHaveBeenCalled();
      });
    }
  }
});

/** The real `setImmediate`, which the fake clock leaves alone: a turn of real I/O. */
const realTurn = () =>
  new Promise<void>((resolve) =>
    (globalThis as unknown as { setImmediate: (callback: () => void) => void }).setImmediate(resolve),
  );

/** Lets the fake clock run `ms`, then lets what it released settle without moving the clock further. */
const elapse = (ms: number) =>
  act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
    for (let round = 0; round < 10; round++) {
      await realTurn();
      await vi.advanceTimersByTimeAsync(0);
    }
  });

describe("PayPage on return from Vipps", () => {
  beforeEach(() => {
    notifyManager.setScheduler((callback) => callback());
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "setInterval", "clearInterval"] });
  });
  afterEach(() => {
    vi.useRealTimers();
    notifyManager.setScheduler((callback) => setTimeout(callback, 0));
  });

  const statusReads = (fetchMock: ReturnType<typeof server>) =>
    fetchMock.actualCalls.filter(([u]) => path(u) === STATUS).length;

  it("polls every 2 s for a minute, then every 5 s, and stops at ten minutes", async () => {
    window.sessionStorage.setItem(PAY_STORAGE_KEY, JSON.stringify({ token: TOKEN }));
    at(`/pay?attempt=${REFERENCE}&nonce=${NONCE}`);
    const fetchMock = server();
    renderWithProviders(<PayPage />);

    await elapse(0);
    expect(statusReads(fetchMock)).toBe(1);
    expect(window.location.search).toBe("");
    // The first minute: a read every two seconds.
    await elapse(PAY_POLL_FAST_MS * (PAY_POLL_FAST_READS - 1));
    expect(statusReads(fetchMock)).toBe(PAY_POLL_FAST_READS);
    await elapse(PAY_POLL_FAST_MS);
    expect(statusReads(fetchMock)).toBe(PAY_POLL_FAST_READS);
    await elapse(PAY_POLL_SLOW_MS - PAY_POLL_FAST_MS);
    expect(statusReads(fetchMock)).toBe(PAY_POLL_FAST_READS + 1);
    // Ten minutes all told: about 150 reads, well inside the 400 per 10 minutes the status allows.
    await elapse(10 * 60_000);
    const total = statusReads(fetchMock);
    expect(total).toBe(PAY_POLL_MAX_READS);
    expect(total).toBeGreaterThan(120);
    expect(total).toBeLessThanOrEqual(150);
    await elapse(10 * 60_000);
    expect(statusReads(fetchMock)).toBe(total);
    expect(screen.getByText(en["pay.state.stillPending"])).toBeInTheDocument();
  });

  it("stops polling once the payment is paid, and says so", async () => {
    window.sessionStorage.setItem(PAY_STORAGE_KEY, JSON.stringify({ token: TOKEN }));
    at(`/pay?attempt=${REFERENCE}&nonce=${NONCE}`);
    let state: PayAttemptStatus["state"] = "pending";
    const fetchMock = server({ status: () => ({ state }) });
    renderWithProviders(<PayPage />);

    await elapse(0);
    state = "paid";
    await elapse(PAY_POLL_FAST_MS);
    expect(screen.getByText(en["pay.state.paid"])).toBeInTheDocument();
    const reads = statusReads(fetchMock);
    await elapse(60_000);
    expect(statusReads(fetchMock)).toBe(reads);
  });

  for (const [state, key] of [
    ["failed", "pay.state.failed"],
    ["cancelled", "pay.state.cancelled"],
  ] as const) {
    it(`says ${state} in words and offers to try again with the same link`, async () => {
      window.sessionStorage.setItem(PAY_STORAGE_KEY, JSON.stringify({ token: TOKEN }));
      at(`/pay?attempt=${REFERENCE}&nonce=${NONCE}`);
      server({ status: () => ({ state }) });
      renderWithProviders(<PayPage />);

      await elapse(0);
      expect(screen.getByText(en[key])).toBeInTheDocument();
      expect(screen.getByRole("button", { name: en["pay.tryAgain"] })).toBeInTheDocument();
    });
  }

  it("in another browser — no token kept — shows the payment's state only", async () => {
    at(`/pay?attempt=${REFERENCE}&nonce=${NONCE}`);
    const fetchMock = server({ status: () => ({ state: "paid" }) });
    renderWithProviders(<PayPage />);

    await elapse(0);
    expect(screen.getByText(en["pay.state.paid"])).toBeInTheDocument();
    expect(fetchMock.actualCalls.map(([u]) => path(u))).toEqual([STATUS]);
    expect(screen.queryByRole("button", { name: en["pay.tryAgain"] })).not.toBeInTheDocument();
  });

  it("says an unknown payment in words and stops asking", async () => {
    at(`/pay?attempt=${REFERENCE}&nonce=${NONCE}`);
    const fetchMock = server({ status: () => new Response(null, { status: 404 }) });
    renderWithProviders(<PayPage />);

    await elapse(0);
    expect(screen.getByText(en["pay.state.notFound"])).toBeInTheDocument();
    await elapse(60_000);
    expect(statusReads(fetchMock)).toBe(1);
  });
});

describe("the pay page's requests", () => {
  it("never carry the token in an API URL, from the read through the return", async () => {
    at(`/pay?token=${TOKEN}`);
    const fetchMock = server({ status: () => ({ state: "paid" }) });
    const first = renderWithProviders(<PayPage />);
    await userEvent.click(await screen.findByRole("checkbox", { name: en["pay.accept"] }));
    await userEvent.click(screen.getByRole("button", { name: en["pay.payWithVipps"] }));
    await waitFor(() => expect(payNavigation.open).toHaveBeenCalled());
    first.unmount();

    // Vipps sends the payer back: the attempt and its nonce in the address, the token kept from before.
    at(`/pay?attempt=${REFERENCE}&nonce=${NONCE}`);
    renderWithProviders(<PayPage />);
    expect(await screen.findByText(en["pay.state.paid"])).toBeInTheDocument();
    expect(window.location.search).toBe("");

    const urls = fetchMock.actualCalls.map(([u]) => path(u));
    expect(urls).toContain(READ);
    expect(urls).toContain(ATTEMPTS);
    expect(urls).toContain(STATUS);
    for (const url of urls) {
      expect(url).not.toContain(TOKEN);
      expect(url).not.toContain(encodeURIComponent(TOKEN));
      // Same-origin only: every request is a path of the pay page's own operations.
      expect(url.startsWith("/api/v1/invoices/pay/")).toBe(true);
    }
  });
});
