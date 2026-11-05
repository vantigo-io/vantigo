import { Anchor, Button, Card, Group, Stack, Table, Text, Title, Tooltip } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconTruckLoading } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { createDelivery, deliveryListQueryOptions } from "../api/deliveries";
import { ORDERS_QUERY_KEY } from "../api/request";
import { refusalMessage } from "../lib/errors";
import { useOrdersFormat } from "../lib/format";
import { deliveryLinkOptions } from "../lib/routes";
import { DeliveryDocumentBadge } from "./status-badges";

export interface DeliveriesCardProps {
  orderId: number;
  /** Whether *Create delivery* is offered: `orders:deliveries-manage`, stock kept and a site. */
  canCreate: boolean;
  /** Why a delivery cannot be made now — on hold, nothing open, unsaved edits — when it cannot. */
  blocked?: string;
}

/**
 * An order's deliveries (orders design O47): each one's number, state and
 * when it was posted, linking to its page, and *Create delivery*, which
 * proposes every open goods line at what is available and opens the draft.
 */
export const DeliveriesCard = ({ orderId, canCreate, blocked }: DeliveriesCardProps) => {
  const { t, dateTime } = useOrdersFormat();
  const navigate = useNavigate() as (options: unknown) => void;
  const queryClient = useQueryClient();
  const deliveries = useQuery(deliveryListQueryOptions({ orderId, pageSize: 50 }));
  const create = useMutation({
    mutationFn: () => createDelivery(orderId),
    onSuccess: async (delivery) => {
      await queryClient.invalidateQueries({ queryKey: [ORDERS_QUERY_KEY, "deliveries"] });
      notifications.show({ color: "green", message: t("deliveryCreated", { number: delivery.number }) });
      navigate(deliveryLinkOptions(delivery.id));
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("couldNotCreateDelivery"), message: refusalMessage(error, t) }),
  });
  const items = deliveries.data?.items ?? [];
  return (
    <Card withBorder padding="lg" data-testid="deliveries-card">
      <Stack gap="md">
        <Group justify="space-between">
          <Title order={4}>{t("orderDeliveries")}</Title>
          {canCreate && (
            <Tooltip label={blocked} disabled={!blocked} withArrow>
              <Button
                size="xs"
                leftSection={<IconTruckLoading size={14} />}
                disabled={Boolean(blocked)}
                loading={create.isPending}
                onClick={() => create.mutate()}
                data-assist="orders.order.create-delivery"
              >
                {t("createDelivery")}
              </Button>
            </Tooltip>
          )}
        </Group>
        {items.length === 0 ? (
          <Text size="sm" c="dimmed">
            {t("noOrderDeliveries")}
          </Text>
        ) : (
          <Table verticalSpacing="xs">
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t("deliveryNumberColumn")}</Table.Th>
                <Table.Th>{t("status")}</Table.Th>
                <Table.Th>{t("postedAt")}</Table.Th>
                <Table.Th ta="right">{t("lines")}</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {items.map((d) => (
                <Table.Tr key={d.id}>
                  <Table.Td>
                    <Anchor size="sm" renderRoot={(props) => <Link {...deliveryLinkOptions(d.id)} {...props} />}>
                      {t("deliveryNumber", { number: d.number })}
                    </Anchor>
                  </Table.Td>
                  <Table.Td>
                    <DeliveryDocumentBadge status={d.status} />
                  </Table.Td>
                  <Table.Td>
                    <Text size="sm">{d.postedAt ? dateTime(d.postedAt) : "—"}</Text>
                  </Table.Td>
                  <Table.Td ta="right">
                    <Text size="sm">{d.lines}</Text>
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        )}
      </Stack>
    </Card>
  );
};
