import type { CustomerResponse } from "../api/customers";

/**
 * Whether a customer takes no more changes: merged into another (customers
 * merge design D4) or anonymised (GDPR design D4) — one scheduled for an
 * anonymisation is not, until the worker has run. The one answer every screen
 * that offers a write on a customer asks — the page header, its cards, the
 * list's pencil, the merge picker, and the host's Projects and Energy tabs — so
 * the next read-only state is taught to all of them at once. An absent customer
 * (still loading) is not read-only: the screen offers what it would anyway, and
 * the server has the last word.
 */
export const isReadOnlyCustomer = (
  customer: Pick<CustomerResponse, "mergedInto" | "anonymisation"> | null | undefined,
): boolean => Boolean(customer?.mergedInto) || Boolean(customer?.anonymisation?.anonymisedAt);
