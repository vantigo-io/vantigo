/**
 * The one path a document lives at. The host owns the routes, but the list
 * links to a document and the create and credit flows navigate to the new
 * draft, so the path is written once, here, and the host's route file and this
 * package's test route tree are both built from it.
 */
export const INVOICE_ROUTE_PATH = "/invoices/$invoiceId";

/** What `navigate` and `Link` take to reach one document. */
export const invoiceLinkOptions = (invoiceId: number) => ({
  to: INVOICE_ROUTE_PATH,
  params: { invoiceId: String(invoiceId) },
});

/** The same path as a plain URL, for a link's href. */
export const invoiceHref = (invoiceId: number): string => INVOICE_ROUTE_PATH.replace("$invoiceId", String(invoiceId));

/** The Payments area: the upload, the accounts, the files and the exception queue (D22). */
export const PAYMENTS_ROUTE_PATH = "/invoices/payments";

/** One imported bank file's result, under the Payments area. */
export const BANK_FILE_ROUTE_PATH = "/invoices/payments/files/$bankFileId";

/** What `navigate` and `Link` take to reach one bank file. */
export const bankFileLinkOptions = (bankFileId: number) => ({
  to: BANK_FILE_ROUTE_PATH,
  params: { bankFileId: String(bankFileId) },
});

/** The same path as a plain URL, for a link's href. */
export const bankFileHref = (bankFileId: number): string =>
  BANK_FILE_ROUTE_PATH.replace("$bankFileId", String(bankFileId));
