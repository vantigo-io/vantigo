import { describe, expect, it } from "vitest";
import { productMessagingOf } from "./product-messaging";

describe("productMessagingOf", () => {
  it("offers nothing without the Products module or its keys", () => {
    expect(productMessagingOf(["products:products-view"], ["communications"])).toMatchObject({
      canAttachProduct: false,
      canViewPricing: false,
    });
    expect(productMessagingOf(["products:pricing-view"], ["communications", "products"])).toMatchObject({
      canAttachProduct: false,
      canViewPricing: false,
    });
  });

  it("offers Attach product with products-view, and the price only beside pricing-view", () => {
    const modules = ["communications", "products"];
    expect(productMessagingOf(["products:products-view"], modules)).toMatchObject({
      canAttachProduct: true,
      canViewPricing: false,
    });
    expect(productMessagingOf(["products:products-view", "products:pricing-view"], modules)).toMatchObject({
      canAttachProduct: true,
      canViewPricing: true,
    });
  });

  it("links to a new order only with Orders mounted and the keys of a new order", () => {
    expect(productMessagingOf(["orders:access", "orders:sales-manage"], ["communications"]).newOrder).toBeUndefined();
    expect(productMessagingOf(["orders:access"], ["communications", "orders"]).newOrder).toBeUndefined();
    const messaging = productMessagingOf(["orders:access", "orders:sales-manage"], ["communications", "orders"]);
    expect(messaging.newOrder?.({ customerId: 1001, productId: 7 })).toEqual({
      to: "/orders/orders/new",
      search: { customerId: 1001, productId: 7 },
    });
    expect(messaging.newOrder?.({ customerId: null, productId: 7 })).toEqual({
      to: "/orders/orders/new",
      search: { productId: 7 },
    });
  });
});
