/** The label for one of the four address types (design D3), in this package's own catalog. */
export const addressTypeLabel = (t: (key: string) => string, type: string): string =>
  type === "invoice"
    ? t("addressTypeInvoice")
    : type === "postal"
      ? t("addressTypePostal")
      : type === "delivery"
        ? t("addressTypeDelivery")
        : type === "visiting"
          ? t("addressTypeVisiting")
          : type;
