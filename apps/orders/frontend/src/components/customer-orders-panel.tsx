import { Alert, Box, Button, Card, Group, SegmentedControl, SimpleGrid, Stack, Text, Title } from "@mantine/core";
import { IconPlus } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageSection } from "@vantigo/frontend-shell";
import { useState } from "react";
import { orderListQueryOptions } from "../api/orders";
import { quoteListQueryOptions } from "../api/quotes";
import { subscriptionListQueryOptions } from "../api/subscriptions";
import { customerSummaryQueryOptions } from "../api/summary";
import type { CurrencySum } from "../api/types";
import "../i18n";
import { refusalMessage } from "../lib/errors";
import { useOrdersFormat } from "../lib/format";
import { newQuoteLinkOptions, newSalesOrderLinkOptions } from "../lib/routes";
import { OrderTable, QuoteTable, SubscriptionTable } from "./document-tables";

export interface CustomerOrdersPanelProps {
  customerId: number;
  /** `orders:quotes-manage`, on a customer neither merged away nor anonymised (O31a). */
  canCreateQuote: boolean;
  /** `orders:sales-manage`, on a customer neither merged away nor anonymised (O31a). */
  canCreateOrder: boolean;
}

/** How many of each list the tab shows; the Orders app has the rest. */
const PANEL_PAGE_SIZE = 10;

/** A summary figure per currency: the sum, and how many documents make it. */
const SummaryTile = ({ label, sums, accent }: { label: string; sums: readonly CurrencySum[]; accent: string }) => {
  const { t, money } = useOrdersFormat();
  return (
    <Card
      withBorder
      padding="md"
      style={{ borderTop: `3px solid var(--mantine-color-${accent}-filled)` }}
      data-testid="summary-tile"
    >
      <Text size="xs" c="dimmed" tt="uppercase" fw={600} style={{ letterSpacing: "0.05em" }}>
        {label}
      </Text>
      {sums.length === 0 ? (
        <Text size="xl" fw={700} ff="monospace" c="dimmed" mt={4}>
          —
        </Text>
      ) : (
        sums.map((sum) => (
          <Group key={sum.currency} justify="space-between" align="baseline" mt={4} wrap="nowrap">
            <Text size="xl" fw={700} ff="monospace">
              {money(sum.amount, sum.currency)}
            </Text>
            <Text size="xs" c="dimmed">
              {t("documentCount", { count: sum.count })}
            </Text>
          </Group>
        ))
      )}
    </Card>
  );
};

/**
 * The customer page's Orders tab (orders design O31a): the summary per
 * currency — open quotes, ordered and not delivered, delivered and not
 * invoiced — then the open quotes (draft and sent) and the orders, open or
 * all, each row linking to the document in the Orders app, and the
 * subscriptions with each one's value per period (O42); the summary counts
 * the active ones at their value per year. *New quote* and *New order* open
 * the editors with the customer filled in.
 */
export const CustomerOrdersPanel = ({ customerId, canCreateQuote, canCreateOrder }: CustomerOrdersPanelProps) => {
  const { t } = useOrdersFormat();
  const [ordersOpen, setOrdersOpen] = useState(true);
  const summary = useQuery(customerSummaryQueryOptions(customerId));
  const quotes = useQuery(quoteListQueryOptions({ customerId, status: ["draft", "sent"], pageSize: PANEL_PAGE_SIZE }));
  const orders = useQuery(
    orderListQueryOptions({ customerId, open: ordersOpen ? true : undefined, pageSize: PANEL_PAGE_SIZE }),
  );
  const subscriptions = useQuery(subscriptionListQueryOptions({ customerId, pageSize: PANEL_PAGE_SIZE }));
  return (
    <Stack gap="lg" data-testid="customer-orders-panel">
      <Group justify="space-between">
        <Title order={3}>{t("customerPanelTitle")}</Title>
        <Group gap="xs">
          {canCreateQuote && (
            <Button
              leftSection={<IconPlus size={16} />}
              renderRoot={(props) => <Link {...newQuoteLinkOptions(customerId)} {...props} />}
              data-assist="orders.customer.new-quote"
            >
              {t("newQuote")}
            </Button>
          )}
          {canCreateOrder && (
            <Button
              variant="default"
              leftSection={<IconPlus size={16} />}
              renderRoot={(props) => <Link {...newSalesOrderLinkOptions(customerId)} {...props} />}
              data-assist="orders.customer.new-order"
            >
              {t("newSalesOrder")}
            </Button>
          )}
        </Group>
      </Group>

      {summary.isPending && <ContentSkeleton rows={1} rowHeight={88} />}
      {summary.isError && <Alert color="red">{refusalMessage(summary.error, t)}</Alert>}
      {summary.data && (
        <SimpleGrid cols={{ base: 1, sm: 2, lg: 4 }}>
          <SummaryTile label={t("summaryOpenQuotes")} sums={summary.data.openQuotes} accent="blue" />
          <SummaryTile label={t("summaryNotDelivered")} sums={summary.data.orderedNotDelivered} accent="yellow" />
          <SummaryTile label={t("summaryNotInvoiced")} sums={summary.data.deliveredNotInvoiced} accent="teal" />
          <SummaryTile label={t("summarySubscriptions")} sums={summary.data.activeSubscriptions ?? []} accent="grape" />
        </SimpleGrid>
      )}

      <PageSection>
        <Stack gap="sm">
          <Title order={4}>{t("openQuotes")}</Title>
          {quotes.isPending && <ContentSkeleton rows={2} rowHeight={36} />}
          {quotes.isError && <Alert color="red">{refusalMessage(quotes.error, t)}</Alert>}
          {quotes.data && quotes.data.items.length === 0 && (
            <EmptyState size="sm" title={t("noOpenQuotes")} description={t("noOpenQuotesDescription")} />
          )}
          {quotes.data && quotes.data.items.length > 0 && (
            <>
              <QuoteTable rows={quotes.data.items} showCustomer={false} />
              {quotes.data.total > quotes.data.items.length && (
                <Text size="xs" c="dimmed">
                  {t("moreInApp", { count: quotes.data.total - quotes.data.items.length })}
                </Text>
              )}
            </>
          )}
        </Stack>
      </PageSection>

      <PageSection>
        <Stack gap="sm">
          <Group justify="space-between">
            <Title order={4}>{t("salesOrdersTitle")}</Title>
            <Box>
              <SegmentedControl
                size="xs"
                aria-label={t("openOrAll")}
                value={ordersOpen ? "open" : "all"}
                onChange={(value) => setOrdersOpen(value === "open")}
                data={[
                  { value: "open", label: t("openOrders") },
                  { value: "all", label: t("allOrders") },
                ]}
              />
            </Box>
          </Group>
          {orders.isPending && <ContentSkeleton rows={2} rowHeight={36} />}
          {orders.isError && <Alert color="red">{refusalMessage(orders.error, t)}</Alert>}
          {orders.data && orders.data.items.length === 0 && (
            <EmptyState
              size="sm"
              title={t("noOrders")}
              description={ordersOpen ? t("noOpenOrdersDescription") : t("noOrdersDescription")}
            />
          )}
          {orders.data && orders.data.items.length > 0 && (
            <>
              <OrderTable rows={orders.data.items} showCustomer={false} />
              {orders.data.total > orders.data.items.length && (
                <Text size="xs" c="dimmed">
                  {t("moreInApp", { count: orders.data.total - orders.data.items.length })}
                </Text>
              )}
            </>
          )}
        </Stack>
      </PageSection>

      <PageSection>
        <Stack gap="sm">
          <Title order={4}>{t("subscriptionsTitle")}</Title>
          {subscriptions.isPending && <ContentSkeleton rows={2} rowHeight={36} />}
          {subscriptions.isError && <Alert color="red">{refusalMessage(subscriptions.error, t)}</Alert>}
          {subscriptions.data && subscriptions.data.items.length === 0 && (
            <EmptyState size="sm" title={t("noSubscriptions")} description={t("noCustomerSubscriptionsDescription")} />
          )}
          {subscriptions.data && subscriptions.data.items.length > 0 && (
            <>
              <SubscriptionTable rows={subscriptions.data.items} showCustomer={false} />
              {subscriptions.data.total > subscriptions.data.items.length && (
                <Text size="xs" c="dimmed">
                  {t("moreInApp", { count: subscriptions.data.total - subscriptions.data.items.length })}
                </Text>
              )}
            </>
          )}
        </Stack>
      </PageSection>
    </Stack>
  );
};
