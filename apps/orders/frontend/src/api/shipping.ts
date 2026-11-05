import { queryOptions } from "@tanstack/react-query";
import { json, ORDERS_QUERY_KEY, request, send } from "./request";
import type {
  CarrierAccount,
  CarrierAccountInput,
  CarrierAccountList,
  CarrierService,
  CarrierVerification,
  Shipment,
  ShipmentUpdate,
  ShippingEstimate,
  ShippingOption,
  SiteCarrierDefaults,
} from "./types";

const accounts = "/api/v1/orders/carrier-accounts";
const shipments = "/api/v1/orders/shipments";

/** The carrier accounts (O51), never their keys; every Orders user reads them to ship. */
export const carrierAccountsQueryOptions = () =>
  queryOptions({
    queryKey: [ORDERS_QUERY_KEY, "carrier-accounts"],
    queryFn: ({ signal }) => request<CarrierAccountList>(accounts, { signal }),
  });

/** The products an account may book, from the carrier's data. */
export const carrierServicesQueryOptions = (accountId: number) =>
  queryOptions({
    queryKey: [ORDERS_QUERY_KEY, "carrier-accounts", "services", accountId],
    queryFn: ({ signal }) => request<{ items: CarrierService[] }>(`${accounts}/${accountId}/services`, { signal }),
    staleTime: 300_000,
  });

/** The default carrier account per site (O51). */
export const siteCarrierDefaultsQueryOptions = () =>
  queryOptions({
    queryKey: [ORDERS_QUERY_KEY, "carrier-accounts", "site-defaults"],
    queryFn: ({ signal }) => request<SiteCarrierDefaults>("/api/v1/orders/shipping/site-defaults", { signal }),
  });

export const createCarrierAccount = (input: CarrierAccountInput) => send<CarrierAccount>(accounts, json("POST", input));
export const updateCarrierAccount = (id: number, input: CarrierAccountInput) =>
  send<CarrierAccount>(`${accounts}/${id}`, json("PUT", input));
export const verifyCarrierAccount = (id: number) =>
  send<CarrierVerification>(`${accounts}/${id}/verify`, { method: "POST" });
export const saveSiteCarrierDefaults = (input: SiteCarrierDefaults) =>
  send<SiteCarrierDefaults>("/api/v1/orders/shipping/site-defaults", json("PUT", input));

/** A delivery's shipments (O50), and an order's, for its tracking links (O52). */
export const deliveryShipmentsQueryOptions = (deliveryId: number) =>
  queryOptions({
    queryKey: [ORDERS_QUERY_KEY, "deliveries", "shipments", deliveryId],
    queryFn: ({ signal }) =>
      request<{ items: Shipment[] }>(`/api/v1/orders/deliveries/${deliveryId}/shipments`, { signal }),
  });

export const orderShipmentsQueryOptions = (orderId: number) =>
  queryOptions({
    queryKey: [ORDERS_QUERY_KEY, "orders", "shipments", orderId],
    queryFn: ({ signal }) => request<{ items: Shipment[] }>(`/api/v1/orders/orders/${orderId}/shipments`, { signal }),
  });

/** The carrier's prices for a draft shipment, asked for on demand. */
export const shipmentRates = (id: number) =>
  send<{ items: ShippingOption[] }>(`${shipments}/${id}/rates`, { method: "GET" });

const token = (read: { version: number; updatedAt: string }) => ({
  version: read.version,
  expectedUpdatedAt: read.updatedAt,
});

export const createShipment = (deliveryId: number) =>
  send<Shipment>(`/api/v1/orders/deliveries/${deliveryId}/shipments`, json("POST", {}));
export const updateShipment = (id: number, input: ShipmentUpdate) =>
  send<Shipment>(`${shipments}/${id}`, json("PUT", input));
export const bookShipment = (shipment: Shipment) =>
  send<Shipment>(`${shipments}/${shipment.id}/book`, json("POST", token(shipment)));
export const cancelShipment = (shipment: Shipment) =>
  send<Shipment>(`${shipments}/${shipment.id}/cancel`, json("POST", token(shipment)));

/** The carrier's label and waybill as it issued them (PDF). */
export const shipmentLabelUrl = (id: number) => `${shipments}/${id}/label`;
export const shipmentWaybillUrl = (id: number) => `${shipments}/${id}/waybill`;

/** A shipping line's price from the carrier with the markup (O53); writes nothing. */
export const estimateShipping = (target: { quoteId: number } | { orderId: number }) =>
  send<ShippingEstimate>("/api/v1/orders/shipping-estimate", json("POST", target));
