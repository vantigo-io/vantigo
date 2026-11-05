/** Whether a path parameter is a positive whole number, so `GET /…/NaN` never reaches the API. */
export const isPositiveId = (value: string) =>
  /^\d+$/.test(value) && Number.isSafeInteger(Number(value)) && Number(value) > 0;

/** The search a new quote's or order's editor takes: the customer, when opened from the customer page (O31a). */
export const validateNewDocumentSearch = (search: Record<string, unknown>): { customerId?: number } => {
  const id = Number(search.customerId);
  return Number.isSafeInteger(id) && id > 0 ? { customerId: id } : {};
};
