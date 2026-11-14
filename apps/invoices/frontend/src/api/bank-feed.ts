import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { INVOICES_QUERY_KEY, json, request } from "./request";

type Schemas = components["schemas"];

/**
 * The bank connection (invoices bank feed design F3, F8): the bank, the API
 * key's last four characters and its status, who connected it and when —
 * never the key — whether the installation allows a bank feed at all, and
 * every account the feed was switched on for.
 */
export type BankFeed = Schemas["InvoicesBankFeed"];
/** One account the feed reads, or read: from which day, its status and its last read. */
export type BankFeedAccount = Schemas["InvoicesBankFeedAccount"];
/** One account the bank answers now, and whether — and why not — it can be switched on. */
export type BankFeedOffer = Schemas["InvoicesBankFeedBankAccount"];
/** PUT /invoices/bank-feed's body: the bank and the API key. */
export type BankFeedInput = Schemas["InvoicesBankFeedRequest"];
/** PUT /invoices/bank-feed/accounts/{account}'s body: on or off, and from which day. */
export type BankFeedAccountInput = Schemas["InvoicesBankFeedAccountRequest"];
/** POST /invoices/bank-feed/sync's answer: the accounts made due. */
export type BankFeedSyncResult = Schemas["InvoicesBankFeedSyncResponse"];

/** The banks the screen offers, in order: Folio, the only one. */
export const BANK_FEED_PROVIDERS = ["folio"] as const;

/** The connection, read with `invoices:access`; it answers no secret. */
export const bankFeedQueryOptions = () =>
  queryOptions({
    queryKey: [INVOICES_QUERY_KEY, "bank-feed"],
    queryFn: ({ signal }) => request<BankFeed>("/api/v1/invoices/bank-feed", { signal }),
  });

/**
 * The accounts the stored key reads, asked of the bank now (`invoices:manage`).
 * It is not retried: a refused key or a bank that cannot be reached is said
 * once, as the refusal it is.
 */
export const bankFeedOffersQueryOptions = () =>
  queryOptions({
    queryKey: [INVOICES_QUERY_KEY, "bank-feed", "offers"],
    queryFn: ({ signal }) => request<{ data: BankFeedOffer[] }>("/api/v1/invoices/bank-feed/accounts", { signal }),
    retry: false,
  });

/**
 * Connects the bank with an API key, verified by the bank before it is stored
 * and never answered again: 422 `bank_feed_credential_rejected` when the bank
 * refuses it, 503 `bank_feed_unavailable` when it cannot be reached, 409
 * `bank_feed_disabled` when the installation allows no bank feed. The caller
 * invalidates `[INVOICES_QUERY_KEY]`.
 */
export const putBankFeed = (input: BankFeedInput): Promise<BankFeed> =>
  request<BankFeed>("/api/v1/invoices/bank-feed", json("PUT", input));

/** Disconnects the bank: the key removed, every account switched off; files and payments stay. */
export const deleteBankFeed = (): Promise<void> => request<void>("/api/v1/invoices/bank-feed", { method: "DELETE" });

/**
 * Switches the feed on or off for one account: on sets its format to `feed`,
 * from `startDay` (today when absent); off leaves the format as it is.
 */
export const putBankFeedAccount = (account: string, input: BankFeedAccountInput): Promise<BankFeedAccount> =>
  request<BankFeedAccount>(`/api/v1/invoices/bank-feed/accounts/${encodeURIComponent(account)}`, json("PUT", input));

/**
 * Fetch now (`invoices:payments`): the account, or every account the feed
 * reads, made due at once — the bank feed worker reads it within a minute.
 * The caller invalidates `[INVOICES_QUERY_KEY]`.
 */
export const postBankFeedSync = (account?: string): Promise<BankFeedSyncResult> =>
  request<BankFeedSyncResult>("/api/v1/invoices/bank-feed/sync", json("POST", account ? { account } : {}));
