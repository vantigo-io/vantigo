# Agents: shipment reads carry the customer scope

The front desk could not answer "where is my parcel?": Orders' shipment and delivery reads
named no customer, so they could not carry `x-vantigo-customer-scope` (virtual employees
design V9b). Spec: `docs/superpowers/specs/2026-11-08-virtual-employees-design.md`.

## Shape

- A single shipment: `OrdersShipment` gains `customerId` (additive); `getShipment` is
  `resource: customerId`. `getDelivery` already carries `customerId`: `resource: customerId`.
- Lists: the dispatcher's `resource` check reads one object, so the lists take an optional
  `customerId` query filter the handler enforces and are `param: customerId`:
  `listDeliveries` (SQL filter), `listOrderShipments` and `listDeliveryShipments` (404 when the
  order's customer differs, so a list by another customer's order id returns nothing).
- The front desk allowlist gains `listOrderShipments`, `getShipment`, `listDeliveries`. Not
  `getDelivery` (marked, but it shows pick locations) and `listDeliveryShipments` (marked; `listOrderShipments` answers the same, so it stays off for scope simplicity); Reminder
  replies shares the front desk's allowlist, so it reads them too (same permissions).

## Tasks

0. The agents tool layer drops the business's freight cost from shipment results (`bookedCost`,
   `bookedCostCurrency`, `bookedCostEstimate`, `carrierAccountName`, `bookedBy`); Orders is unchanged.

1. `openapi/orders.yaml` (+ spec copy, generated client): field, filter, scope marks.
2. `queries/deliveries.sql`, `deliveries.go`, `shipments.go`.
3. `agents/template.go` allowlist and comment.
4. Tests: Orders handler test; `customerDataOperations`; integration
   `agents_shipments_test.go`; the template pinning test.
5. Documentation: `reference/orders.md`, `reference/agents.md`, `user/agents.md` (en, nb);
   then `go generate ./...`.
