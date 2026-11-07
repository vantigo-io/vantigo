import { createContext, createElement, type ReactNode, useContext } from "react";

/** Where a link to Orders' new-order page goes, and with what: the customer and the product (Messenger design N17). */
export type NewOrderTarget = { to: string; search: Record<string, number> };

/**
 * What the host tells the chat about the other modules it sits beside (Messenger design N16, N17): whether
 * the user may pick a product, whether they may read prices, and where *Create order* goes. The host
 * judges each from the modules mounted and the user's permissions; the default is nothing, so a chat
 * mounted without a host offers no product and no link.
 */
export type ProductMessaging = {
  /** Products is mounted and the user holds products:products-view: *Attach product* is offered. */
  canAttachProduct: boolean;
  /** The user holds products:pricing-view: a card's subtitle is prefilled with the price. */
  canViewPricing: boolean;
  /**
   * The new-order page for a product, with the conversation's customer when it has one. Absent when Orders
   * is not mounted or the user may not create orders.
   */
  newOrder?: (target: { customerId: number | null; productId: number }) => NewOrderTarget;
};

export const noProductMessaging: ProductMessaging = { canAttachProduct: false, canViewPricing: false };

const ProductMessagingContext = createContext<ProductMessaging>(noProductMessaging);

export const ProductMessagingProvider = ({ value, children }: { value: ProductMessaging; children: ReactNode }) =>
  createElement(ProductMessagingContext.Provider, { value }, children);

export const useProductMessaging = (): ProductMessaging => useContext(ProductMessagingContext);
