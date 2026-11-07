/** Whether a path parameter is a positive whole number, so `GET /…/NaN` never reaches the API. */
export const isPositiveId = (value: string) =>
  /^\d+$/.test(value) && Number.isSafeInteger(Number(value)) && Number(value) > 0;

const positiveId = (value: unknown): number | undefined => {
  const id = Number(value);
  return Number.isSafeInteger(id) && id > 0 ? id : undefined;
};

/**
 * The search a new quote's or order's editor takes: the customer, when opened from the customer page
 * (O31a), and, when opened from a message in Communications that showed a product, the product or its
 * variant the order starts with (Messenger design N17).
 */
export const validateNewDocumentSearch = (
  search: Record<string, unknown>,
): { customerId?: number; productId?: number; variantId?: number } => {
  const customerId = positiveId(search.customerId);
  const productId = positiveId(search.productId);
  const variantId = positiveId(search.variantId);
  return {
    ...(customerId !== undefined ? { customerId } : {}),
    ...(productId !== undefined ? { productId } : {}),
    ...(variantId !== undefined ? { variantId } : {}),
  };
};
