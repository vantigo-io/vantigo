import { Anchor, Badge, Button, Group, Stack, Text } from "@mantine/core";
import { IconBuildingFactory2 } from "@tabler/icons-react";
import { Link } from "@tanstack/react-router";
import type { Line } from "../api/types";
import type { OrdersAccess } from "../lib/access";
import { useOrdersFormat } from "../lib/format";
import { remainingToDeliver } from "../lib/lines";
import { manufacturingOrderLinkOptions, newManufacturingOrderLinkOptions } from "../lib/routes";

/** The statuses Manufacturing's orders go through, as the line names them. */
const statusColor: Record<string, string> = {
  draft: "gray",
  confirmed: "blue",
  in_progress: "yellow",
  done: "teal",
};

/** The reasons make-to-order leaves a line out that the line names in the viewer's language; any other shows Manufacturing's text. */
const notMadeReasons = new Set([
  "no_site",
  "nothing_open",
  "variant_not_goods",
  "sales_order_line_has_order",
  "failed",
]);

/**
 * What Manufacturing makes for a goods line (manufacturing design M45, M48a):
 * each manufacturing order made for it — its number, linking to it, its
 * status, what is produced of its quantity and its planned finish —, *Create
 * manufacturing order* on a line whose product has a bill of materials and no
 * order yet, why make-to-order made none (M44a) to a caller who sees
 * Manufacturing, and, once one closed, that the goods are ready for *Reserve
 * now* — or that they were made at another site and must be moved first.
 */
export const LineProduction = ({ line, access }: { line: Line; access: OrdersAccess }) => {
  const { t, quantity, date, dateTime } = useOrdersFormat();
  const made = line.production ?? [];
  const ready = line.readyToReserve;
  const unreserved = remainingToDeliver(line) > (line.qtyReserved ?? 0);
  const offerCreate = access.canCreateManufacturingOrders && line.hasBom === true && made.length === 0;
  const notMade = access.canViewManufacturing && made.length === 0 ? line.notMade : undefined;
  if (made.length === 0 && !offerCreate && !notMade && !(ready && unreserved)) return null;
  return (
    <Stack gap={4} data-testid="line-production">
      {made.map((order) => (
        <Group key={order.manufacturingOrderId} gap={6} wrap="nowrap">
          <IconBuildingFactory2 size={14} aria-hidden />
          {access.canViewManufacturing ? (
            <Anchor
              size="xs"
              renderRoot={(props) => <Link {...manufacturingOrderLinkOptions(order.manufacturingOrderId)} {...props} />}
            >
              {order.number}
            </Anchor>
          ) : (
            <Text size="xs">{order.number}</Text>
          )}
          <Badge size="xs" variant="light" color={statusColor[order.status] ?? "gray"}>
            {statusColor[order.status] ? t(`manufacturingStatus.${order.status}`) : order.status}
          </Badge>
          <Text size="xs" c="dimmed">
            {t("producedOf", { produced: quantity(order.produced), quantity: quantity(order.quantity) })}
            {order.plannedFinish
              ? ` · ${t("plannedFinish", { when: dateTime(order.plannedFinish) })}`
              : order.dueDate
                ? ` · ${t("manufacturingDue", { date: date(order.dueDate) })}`
                : ""}
          </Text>
        </Group>
      ))}
      {notMade && (
        <Text size="xs" c="orange" data-testid="line-not-made">
          {t("noManufacturingOrderMade", {
            reason: notMadeReasons.has(notMade.code) ? t(`notMadeReason.${notMade.code}`) : notMade.detail,
          })}
        </Text>
      )}
      {offerCreate && (
        <Button
          size="compact-xs"
          variant="light"
          w="fit-content"
          leftSection={<IconBuildingFactory2 size={14} />}
          renderRoot={(props) => <Link {...newManufacturingOrderLinkOptions(line.id)} {...props} />}
          data-assist="orders.lines.create-manufacturing-order"
        >
          {t("createManufacturingOrder")}
        </Button>
      )}
      {ready && unreserved && (
        <Text size="xs" c={ready.otherSite ? "orange" : "teal"} data-testid="line-ready">
          {ready.otherSite
            ? t("producedOtherSite", { quantity: quantity(ready.produced) })
            : t("producedReadyToReserve", { quantity: quantity(ready.produced) })}
        </Text>
      )}
    </Stack>
  );
};
