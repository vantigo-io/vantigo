import { Anchor, Group, Table, Text } from "@mantine/core";
import { Link } from "@tanstack/react-router";
import type { OrderSummary, QuoteSummary, SubscriptionSummary } from "../api/types";
import { useOrdersFormat } from "../lib/format";
import { quoteLinkOptions, salesOrderLinkOptions, subscriptionLinkOptions } from "../lib/routes";
import {
  DeliveryBadge,
  InvoiceBadge,
  OrderFlags,
  OrderStatusBadge,
  QuoteStatusBadge,
  SubscriptionStatusBadge,
} from "./status-badges";

/** Whether a sent quote's valid-until has passed: it is the server's worker that expires it, this only warns. */
const lapsed = (quote: QuoteSummary, today: string) =>
  quote.status === "sent" && quote.validUntil != null && quote.validUntil < today;

/** Quotes in a table (O20): number and revision, customer, state, valid until, salesperson and the amounts. */
export const QuoteTable = ({
  rows,
  showCustomer = true,
}: {
  rows: readonly QuoteSummary[];
  showCustomer?: boolean;
}) => {
  const { t, money, date } = useOrdersFormat();
  const today = new Date().toISOString().slice(0, 10);
  return (
    <Table.ScrollContainer minWidth={720}>
      <Table highlightOnHover verticalSpacing="xs" data-testid="quote-table">
        <Table.Thead>
          <Table.Tr>
            <Table.Th>{t("number")}</Table.Th>
            {showCustomer && <Table.Th>{t("customer")}</Table.Th>}
            <Table.Th>{t("status")}</Table.Th>
            <Table.Th>{t("validUntil")}</Table.Th>
            <Table.Th>{t("salesperson")}</Table.Th>
            <Table.Th ta="right">{t("totalNet")}</Table.Th>
            <Table.Th ta="right">{t("totalGross")}</Table.Th>
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {rows.map((quote) => (
            <Table.Tr key={quote.id}>
              <Table.Td>
                <Anchor
                  fw={600}
                  ff="monospace"
                  renderRoot={(props) => <Link {...quoteLinkOptions(quote.id)} {...props} />}
                >
                  {quote.number}
                </Anchor>
                {quote.revision > 1 && (
                  <Text span size="xs" c="dimmed" ml={4}>
                    {t("revisionShort", { revision: quote.revision })}
                  </Text>
                )}
              </Table.Td>
              {showCustomer && <Table.Td>{quote.customerName}</Table.Td>}
              <Table.Td>
                <QuoteStatusBadge status={quote.status} />
              </Table.Td>
              <Table.Td>
                <Text size="sm" c={lapsed(quote, today) ? "orange" : undefined}>
                  {quote.validUntil ? date(quote.validUntil) : "—"}
                </Text>
              </Table.Td>
              <Table.Td>
                <Text size="sm">{quote.salesperson?.displayName ?? "—"}</Text>
              </Table.Td>
              <Table.Td ta="right" ff="monospace">
                {money(quote.net, quote.currency)}
              </Table.Td>
              <Table.Td ta="right" ff="monospace" fw={600}>
                {money(quote.gross, quote.currency)}
              </Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
    </Table.ScrollContainer>
  );
};

/** Orders in a table (O18): number, customer, state with its flags, delivery and invoice status, and the amounts. */
export const OrderTable = ({
  rows,
  showCustomer = true,
}: {
  rows: readonly OrderSummary[];
  showCustomer?: boolean;
}) => {
  const { t, money, date } = useOrdersFormat();
  return (
    <Table.ScrollContainer minWidth={820}>
      <Table highlightOnHover verticalSpacing="xs" data-testid="order-table">
        <Table.Thead>
          <Table.Tr>
            <Table.Th>{t("number")}</Table.Th>
            {showCustomer && <Table.Th>{t("customer")}</Table.Th>}
            <Table.Th>{t("status")}</Table.Th>
            <Table.Th>{t("deliveryColumn")}</Table.Th>
            <Table.Th>{t("invoicingColumn")}</Table.Th>
            <Table.Th>{t("deliveryDate")}</Table.Th>
            <Table.Th ta="right">{t("totalNet")}</Table.Th>
            <Table.Th ta="right">{t("totalGross")}</Table.Th>
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {rows.map((order) => (
            <Table.Tr key={order.id}>
              <Table.Td>
                <Anchor
                  fw={600}
                  ff="monospace"
                  renderRoot={(props) => <Link {...salesOrderLinkOptions(order.id)} {...props} />}
                >
                  {order.number}
                </Anchor>
              </Table.Td>
              {showCustomer && <Table.Td>{order.customerName}</Table.Td>}
              <Table.Td>
                <Group gap={4} wrap="nowrap">
                  <OrderStatusBadge status={order.status} />
                  <OrderFlags done={order.done} onHold={order.onHold} locked={order.locked} />
                </Group>
              </Table.Td>
              <Table.Td>{order.status === "confirmed" && <DeliveryBadge status={order.deliveryStatus} />}</Table.Td>
              <Table.Td>{order.status === "confirmed" && <InvoiceBadge status={order.invoiceStatus} />}</Table.Td>
              <Table.Td>
                <Text size="sm">{order.deliveryDate ? date(order.deliveryDate) : "—"}</Text>
              </Table.Td>
              <Table.Td ta="right" ff="monospace">
                {money(order.net, order.currency)}
              </Table.Td>
              <Table.Td ta="right" ff="monospace" fw={600}>
                {money(order.gross, order.currency)}
              </Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
    </Table.ScrollContainer>
  );
};

/**
 * Subscriptions in a table (O42): number, customer, state, plan, start and
 * end, billed through and a full period's amounts.
 */
export const SubscriptionTable = ({
  rows,
  showCustomer = true,
}: {
  rows: readonly SubscriptionSummary[];
  showCustomer?: boolean;
}) => {
  const { t, money, date } = useOrdersFormat();
  return (
    <Table.ScrollContainer minWidth={820}>
      <Table highlightOnHover verticalSpacing="xs" data-testid="subscription-table">
        <Table.Thead>
          <Table.Tr>
            <Table.Th>{t("number")}</Table.Th>
            {showCustomer && <Table.Th>{t("customer")}</Table.Th>}
            <Table.Th>{t("status")}</Table.Th>
            <Table.Th>{t("plan")}</Table.Th>
            <Table.Th>{t("startDate")}</Table.Th>
            <Table.Th>{t("billedThrough")}</Table.Th>
            <Table.Th ta="right">{t("perPeriodNet")}</Table.Th>
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {rows.map((sub) => (
            <Table.Tr key={sub.id}>
              <Table.Td>
                <Anchor
                  fw={600}
                  ff="monospace"
                  renderRoot={(props) => <Link {...subscriptionLinkOptions(sub.id)} {...props} />}
                >
                  {sub.number}
                </Anchor>
              </Table.Td>
              {showCustomer && <Table.Td>{sub.customerName}</Table.Td>}
              <Table.Td>
                <SubscriptionStatusBadge status={sub.status} />
              </Table.Td>
              <Table.Td>
                <Text size="sm">
                  {t(`interval.${sub.interval}`)} · {t(`billing.${sub.billing}`)}
                </Text>
              </Table.Td>
              <Table.Td>
                <Text size="sm">
                  {date(sub.startDate)}
                  {sub.endDate ? ` – ${date(sub.endDate)}` : ""}
                </Text>
              </Table.Td>
              <Table.Td>
                <Text size="sm">{sub.billedThrough ? date(sub.billedThrough) : "—"}</Text>
              </Table.Td>
              <Table.Td ta="right" ff="monospace">
                {money(sub.net, sub.currency)}
              </Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
    </Table.ScrollContainer>
  );
};
