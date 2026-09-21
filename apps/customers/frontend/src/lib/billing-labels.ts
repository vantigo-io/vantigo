/**
 * Catalog lookups for the billing profile's enum-ish fields (design D4, D6):
 * every delivery method both `invoiceDelivery` and `reminderDelivery` can
 * name, the two document languages, and the four warning codes GET
 * .../billing-profile computes. Shared between the read-only card and the
 * edit modal's `<Select>` options, the same reason `addressTypeLabel` is
 * shared between the address list and its modal.
 */

/** Every value `invoiceDelivery` can hold; `reminderDelivery` offers only "email" and "paper" of these. */
export const deliveryMethodLabel = (t: (key: string) => string, method: string): string => {
  switch (method) {
    case "email":
      return t("deliveryMethodEmail");
    case "ehf":
      return t("deliveryMethodEhf");
    case "efaktura":
      return t("deliveryMethodEfaktura");
    case "paper":
      return t("deliveryMethodPaper");
    default:
      return method;
  }
};

export const billingLanguageLabel = (t: (key: string) => string, language: string): string =>
  language === "nb" ? t("billingLanguageNb") : language === "en" ? t("billingLanguageEn") : language;

/**
 * A warning code's catalog key (design D4). Unknown codes have no entry and
 * must be filtered out by the caller before calling this — see
 * `KNOWN_BILLING_WARNING_CODES`.
 */
const WARNING_MESSAGE_KEYS: Record<string, string> = {
  ehf_without_recipient: "warningEhfWithoutRecipient",
  email_without_address: "warningEmailWithoutAddress",
  efaktura_for_business: "warningEfakturaForBusiness",
  no_invoice_address: "warningNoInvoiceAddress",
};

/** Every warning code GET .../billing-profile can send (design D4) — anything else is ignored. */
export const KNOWN_BILLING_WARNING_CODES: readonly string[] = Object.keys(WARNING_MESSAGE_KEYS);

export const billingWarningMessage = (t: (key: string) => string, code: string): string | null => {
  const key = WARNING_MESSAGE_KEYS[code];
  return key ? t(key) : null;
};
