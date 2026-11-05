import { Button, Card, Group, Stack, Title } from "@mantine/core";
import { IconPlus } from "@tabler/icons-react";
import { Link } from "@tanstack/react-router";
import { EmptyState, useI18n } from "@vantigo/frontend-shell";
import { newQuoteLinkOptions, newSalesOrderLinkOptions } from "../lib/routes";

export interface CustomerOrdersPanelProps {
  customerId: number;
  /** `orders:quotes-manage`, on a customer neither merged away nor anonymised (O31a). */
  canCreateQuote: boolean;
  /** `orders:sales-manage`, on a customer neither merged away nor anonymised (O31a). */
  canCreateOrder: boolean;
}

/**
 * The customer page's Orders tab (orders design O31a): the summary, the open
 * quotes, the orders and, from S, the subscriptions of one customer, with
 * *New quote* and *New order* opening the editors with the customer filled in.
 */
export const CustomerOrdersPanel = ({ customerId, canCreateQuote, canCreateOrder }: CustomerOrdersPanelProps) => {
  const { t } = useI18n("orders");
  return (
    <Card withBorder>
      <Stack gap="md">
        <Group justify="space-between">
          <Title order={3}>{t("customerPanelTitle")}</Title>
          <Group gap="xs">
            {canCreateQuote && (
              <Button
                leftSection={<IconPlus size={16} />}
                renderRoot={(props) => <Link {...newQuoteLinkOptions(customerId)} {...props} />}
              >
                {t("newQuote")}
              </Button>
            )}
            {canCreateOrder && (
              <Button
                variant="default"
                leftSection={<IconPlus size={16} />}
                renderRoot={(props) => <Link {...newSalesOrderLinkOptions(customerId)} {...props} />}
              >
                {t("newSalesOrder")}
              </Button>
            )}
          </Group>
        </Group>
        <EmptyState size="sm" title={t("notReadyTitle")} description={t("notReadyDescription")} />
      </Stack>
    </Card>
  );
};
