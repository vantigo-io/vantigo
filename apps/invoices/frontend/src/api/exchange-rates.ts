import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { INVOICES_QUERY_KEY, json, request } from "./request";

type Schemas = components["schemas"];

/** A currency's rate of one day, NOK per one unit (invoices phase 6 design I18). */
export type ExchangeRate = Schemas["InvoicesExchangeRate"];
/** One enabled currency's last fetch from Norges Bank. */
export type RateFetch = Schemas["InvoicesRateFetch"];

/**
 * The currencies Norges Bank publishes a daily rate of against NOK — the
 * server's list (`nbCurrencies`), which the settings may turn on.
 */
export const NORGES_BANK_CURRENCIES = [
  "AUD",
  "BDT",
  "BRL",
  "BYN",
  "CAD",
  "CHF",
  "CNY",
  "CZK",
  "DKK",
  "EUR",
  "GBP",
  "HKD",
  "HUF",
  "IDR",
  "ILS",
  "INR",
  "ISK",
  "JPY",
  "KRW",
  "MMK",
  "MXN",
  "MYR",
  "NZD",
  "PHP",
  "PKR",
  "PLN",
  "RON",
  "SEK",
  "SGD",
  "THB",
  "TRY",
  "TWD",
  "USD",
  "VND",
  "ZAR",
] as const;

/** A currency's rates from `from` to `to`, the newest first; the server defaults the last 30 days. */
export const exchangeRatesQueryOptions = (currency: string) =>
  queryOptions({
    queryKey: [INVOICES_QUERY_KEY, "exchange-rates", currency],
    queryFn: ({ signal }) =>
      request<Schemas["InvoicesExchangeRateList"]>(
        `/api/v1/invoices/exchange-rates?currency=${encodeURIComponent(currency)}`,
        {
          signal,
        },
      ),
  });

/** Enters a manual rate of a day, which a fetch never overwrites. */
export const putManualRate = (currency: string, day: string, rate: number): Promise<ExchangeRate> =>
  request<ExchangeRate>(`/api/v1/invoices/exchange-rates/${currency}/${day}`, json("PUT", { rate }));

/** Removes a manual rate; a fetched one is refused `exchange_rate_fetched`. */
export const removeManualRate = (currency: string, day: string) =>
  request<void>(`/api/v1/invoices/exchange-rates/${currency}/${day}`, { method: "DELETE" });

/** Fetch now: one cycle of the invoices-rates worker, answering every fetch row. */
export const fetchRatesNow = () =>
  request<Schemas["InvoicesRateFetchResponse"]>("/api/v1/invoices/exchange-rates/fetch", { method: "POST" });
