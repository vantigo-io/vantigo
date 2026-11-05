import { Badge, Group } from "@mantine/core";
import { IconCircleCheck, IconLock, IconPlayerPause } from "@tabler/icons-react";
import type {
  DeliveryStatus,
  InvoiceStatus,
  OrderStatus,
  PeriodState,
  QuoteStatus,
  SubscriptionStatus,
} from "../api/types";
import { hasWords } from "../lib/errors";
import { useOrdersFormat } from "../lib/format";

/** The colour each state is shown in; a state a newer server added is grey. */
const quoteColours: Record<string, string> = {
  draft: "gray",
  sent: "blue",
  accepted: "green",
  declined: "red",
  expired: "orange",
  cancelled: "gray",
};
const orderColours: Record<string, string> = { draft: "gray", confirmed: "blue", cancelled: "gray" };
const subscriptionColours: Record<string, string> = {
  draft: "gray",
  active: "green",
  paused: "orange",
  ended: "blue",
  cancelled: "gray",
};
const periodColours: Record<string, string> = {
  invoiced: "teal",
  draft: "blue",
  skipped: "gray",
  paused: "orange",
  due: "yellow",
  upcoming: "gray",
};
const progressColours: Record<string, string> = {
  none: "gray",
  partial: "yellow",
  delivered: "teal",
  invoiced: "teal",
};

const Words = ({ prefix, value }: { prefix: string; value: string }) => {
  const { t } = useOrdersFormat();
  return <>{hasWords(`${prefix}.${value}`) ? t(`${prefix}.${value}`) : value}</>;
};

/** A quote's state (O20) as a badge. The server judges the state; this only words and colours it. */
export const QuoteStatusBadge = ({ status }: { status: QuoteStatus }) => (
  <Badge variant="light" color={quoteColours[status] ?? "gray"} data-testid="quote-status" data-status={status}>
    <Words prefix="quoteStatus" value={status} />
  </Badge>
);

/** An order's lifecycle (O18) as a badge. */
export const OrderStatusBadge = ({ status }: { status: OrderStatus }) => (
  <Badge variant="light" color={orderColours[status] ?? "gray"} data-testid="order-status" data-status={status}>
    <Words prefix="orderStatus" value={status} />
  </Badge>
);

/** A subscription's state (O42) as a badge. */
export const SubscriptionStatusBadge = ({ status }: { status: SubscriptionStatus }) => (
  <Badge
    variant="light"
    color={subscriptionColours[status] ?? "gray"}
    data-testid="subscription-status"
    data-status={status}
  >
    <Words prefix="subscriptionStatus" value={status} />
  </Badge>
);

/** A billing period's state (O43): invoiced, on a draft invoice, skipped, paused, due or upcoming. */
export const PeriodStateBadge = ({ state }: { state: PeriodState }) => (
  <Badge variant="dot" color={periodColours[state] ?? "gray"} data-testid="period-state" data-state={state}>
    <Words prefix="periodState" value={state} />
  </Badge>
);

/** How far an order is delivered (O18). */
export const DeliveryBadge = ({ status }: { status: DeliveryStatus }) => (
  <Badge variant="dot" color={progressColours[status] ?? "gray"} data-testid="delivery-status">
    <Words prefix="deliveryStatus" value={status} />
  </Badge>
);

/** How far an order is invoiced (O18). */
export const InvoiceBadge = ({ status }: { status: InvoiceStatus }) => (
  <Badge variant="dot" color={progressColours[status] ?? "gray"} data-testid="invoice-status">
    <Words prefix="invoiceStatus" value={status} />
  </Badge>
);

/** The flags beside an order's state: done (derived, never stored), on hold and locked (O18). */
export const OrderFlags = ({ done, onHold, locked }: { done: boolean; onHold: boolean; locked: boolean }) => {
  const { t } = useOrdersFormat();
  return (
    <Group gap={4} wrap="nowrap">
      {done && (
        <Badge color="green" variant="filled" leftSection={<IconCircleCheck size={12} />}>
          {t("done")}
        </Badge>
      )}
      {onHold && (
        <Badge color="orange" variant="light" leftSection={<IconPlayerPause size={12} />}>
          {t("onHold")}
        </Badge>
      )}
      {locked && (
        <Badge color="grape" variant="light" leftSection={<IconLock size={12} />}>
          {t("locked")}
        </Badge>
      )}
    </Group>
  );
};

const deliveryDocumentColours: Record<string, string> = { draft: "gray", posted: "teal", cancelled: "gray" };

/** A delivery document's state (O47) as a badge. */
export const DeliveryDocumentBadge = ({ status }: { status: string }) => (
  <Badge
    variant="light"
    color={deliveryDocumentColours[status] ?? "gray"}
    data-testid="delivery-status"
    data-status={status}
  >
    <Words prefix="deliveryDocumentStatus" value={status} />
  </Badge>
);
