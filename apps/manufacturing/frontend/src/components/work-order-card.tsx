import { Badge, Card, Group, Stack, Text } from "@mantine/core";
import { Link } from "@tanstack/react-router";
import { isClosedWorkOrder, type ManufacturingWorkOrder } from "../api/shop-floor";
import { reservationColor, useManufacturingFormat, workOrderStatusColor } from "../lib/format";
import { workOrderLinkOptions } from "../lib/routes";
import { operationProgress, TOUCH_CARD_MIN_HEIGHT } from "../lib/shop-floor";

/** Who runs a work order: the names of those with an open log on it. */
const operatorsText = (workOrder: ManufacturingWorkOrder) =>
  workOrder.operators.map((operator) => operator.user.displayName).join(", ");

/**
 * One work order of a queue or a list as a large card linking to its view
 * (M31); with `onOpen`, a button that opens it in place instead (a station's
 * page, which links nowhere). It shows the good units the work order has
 * registered of the order's quantity, and what is left while it is open.
 */
export const WorkOrderCard = ({ workOrder, onOpen }: { workOrder: ManufacturingWorkOrder; onOpen?: () => void }) => {
  const { t, quantity, day } = useManufacturingFormat();
  const { order } = workOrder;
  const running = operatorsText(workOrder);
  const progress = operationProgress(workOrder);
  return (
    <Card
      withBorder
      padding="lg"
      mih={TOUCH_CARD_MIN_HEIGHT}
      renderRoot={(props) =>
        onOpen ? (
          <button
            type="button"
            {...props}
            onClick={onOpen}
            style={{ ...props.style, textAlign: "start", width: "100%" }}
          />
        ) : (
          <Link {...workOrderLinkOptions(workOrder.id)} {...props} />
        )
      }
      aria-label={`${order.number} · ${workOrder.name}`}
    >
      <Group justify="space-between" align="flex-start" wrap="wrap" gap="sm">
        <Stack gap={2}>
          <Group gap="sm">
            <Text size="xl" fw={700}>
              {order.number}
            </Text>
            <Badge size="lg" variant="light" color={workOrderStatusColor[workOrder.status]}>
              {t(`workOrderStatus_${workOrder.status}`)}
            </Badge>
            {order.priority === "urgent" && (
              <Badge size="lg" color="red">
                {t("priority_urgent")}
              </Badge>
            )}
          </Group>
          <Text size="lg">{`${order.variant.sku} · ${order.variant.productName}`}</Text>
          <Text size="md" c="dimmed">
            {t("operationOf", { sequence: workOrder.sequence, name: workOrder.name })}
          </Text>
        </Stack>
        <Stack gap={4} align="flex-end">
          <Text size="lg" fw={600}>
            {t("doneOfQuantity", {
              done: quantity(progress.done),
              quantity: quantity(order.quantity, order.variant.unit),
            })}
          </Text>
          {!isClosedWorkOrder(workOrder) && (
            <Text size="md">{t("remainingValue", { quantity: quantity(progress.remaining, order.variant.unit) })}</Text>
          )}
          <Text size="md">{t("dueValue", { date: order.dueDate ? day(order.dueDate) : "—" })}</Text>
          {order.reservationStatus && (
            <Badge size="lg" variant="light" color={reservationColor[order.reservationStatus]}>
              {t(`reservation_${order.reservationStatus}`)}
            </Badge>
          )}
        </Stack>
      </Group>
      {running && (
        <Text size="md" mt="sm">
          {t("runBy", { names: running })}
        </Text>
      )}
    </Card>
  );
};
