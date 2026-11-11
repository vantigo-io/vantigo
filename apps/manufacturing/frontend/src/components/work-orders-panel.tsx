import { Alert, Anchor, Badge, Button, Group, Stack, Table, Text } from "@mantine/core";
import { IconAlertCircle, IconPencil, IconPlus } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ContentSkeleton, PageSection } from "@vantigo/frontend-shell";
import { useState } from "react";
import type { ManufacturingOrder } from "../api/orders";
import {
  type ManufacturingTimeLog,
  type ManufacturingWorkOrder,
  orderTimeLogsQueryOptions,
  orderWorkOrdersQueryOptions,
} from "../api/shop-floor";
import { useManufacturingFormat, workOrderStatusColor } from "../lib/format";
import { workOrderLinkOptions } from "../lib/routes";
import { TimeLogDialog } from "./time-log-dialog";

/**
 * A work order's duration deviation (M41): actual against expected, signed,
 * with its per cent. Nothing to say before any time ran on it, unless it is done.
 */
const deviationText = (
  workOrder: ManufacturingWorkOrder,
  minutes: (value: number) => string,
  decimal: (text: string) => string,
): string | null => {
  if (workOrder.actualMinutes === 0 && workOrder.status !== "done") return null;
  const sign = workOrder.deviationMinutes > 0 ? "+" : "";
  const amount = `${sign}${minutes(workOrder.deviationMinutes)}`;
  if (workOrder.deviationPercent === null) return amount;
  const percentSign = workOrder.deviationMinutes > 0 ? "+" : "";
  return `${amount} (${percentSign}${decimal(workOrder.deviationPercent)} %)`;
};

/** A closed log's whole minutes; null while it runs. */
const logMinutes = (log: ManufacturingTimeLog) =>
  log.endedAt ? Math.floor((new Date(log.endedAt).getTime() - new Date(log.startedAt).getTime()) / 60_000) : null;

export interface WorkOrdersPanelProps {
  order: ManufacturingOrder;
  live: boolean;
  /** Whether the caller may correct and add time logs: `manufacturing:orders-manage`. */
  canManage: boolean;
}

/**
 * An order's work orders and their time logs (M27, M29), the order page's
 * Work orders tab: each work order with its status and its logged against
 * expected minutes, its actual time and its deviation (M41); every log, by start, with who, what kind, when and why
 * it was corrected; and, for a supervisor, Correct on a log and Add missing
 * log. Read again on the order's live event.
 */
export const WorkOrdersPanel = ({ order, live, canManage }: WorkOrdersPanelProps) => {
  const { t, stamp, minutes, decimal } = useManufacturingFormat();
  const workOrders = useQuery(orderWorkOrdersQueryOptions(order.id, live));
  const logs = useQuery(orderTimeLogsQueryOptions(order.id, live));
  const [correcting, setCorrecting] = useState<ManufacturingTimeLog>();
  const [adding, setAdding] = useState(false);
  const closed = order.status === "done" || order.status === "cancelled";
  const editable = canManage && !closed;
  const nameOf = new Map((workOrders.data ?? []).map((one) => [one.id, `${one.sequence} · ${one.name}`]));

  return (
    <Stack gap="lg">
      <PageSection title={t("workOrders")}>
        <div data-assist="manufacturing.order.work-orders">
          {workOrders.error ? (
            <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
              {workOrders.error.message}
            </Alert>
          ) : workOrders.isPending ? (
            <ContentSkeleton rows={2} />
          ) : workOrders.data.length === 0 ? (
            <Text size="sm" c="dimmed">
              {t("noWorkOrders")}
            </Text>
          ) : (
            <Table.ScrollContainer minWidth={900}>
              <Table>
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>{t("sequence")}</Table.Th>
                    <Table.Th>{t("operationName")}</Table.Th>
                    <Table.Th>{t("workCenter")}</Table.Th>
                    <Table.Th>{t("status")}</Table.Th>
                    <Table.Th>{t("loggedOfExpected")}</Table.Th>
                    <Table.Th>{t("actualTime")}</Table.Th>
                    <Table.Th>{t("durationDeviation")}</Table.Th>
                    <Table.Th>{t("runningNow")}</Table.Th>
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {workOrders.data.map((workOrder) => {
                    const deviation = deviationText(workOrder, minutes, decimal);
                    return (
                      <Table.Tr key={workOrder.id}>
                        <Table.Td>{workOrder.sequence}</Table.Td>
                        <Table.Td>
                          <Anchor
                            size="sm"
                            renderRoot={(props) => <Link {...workOrderLinkOptions(workOrder.id)} {...props} />}
                          >
                            {workOrder.name}
                          </Anchor>
                        </Table.Td>
                        <Table.Td>{workOrder.workCenter.code}</Table.Td>
                        <Table.Td>
                          <Badge variant="light" color={workOrderStatusColor[workOrder.status]}>
                            {t(`workOrderStatus_${workOrder.status}`)}
                          </Badge>
                        </Table.Td>
                        <Table.Td>
                          {t("minutesOf", {
                            logged: minutes(workOrder.loggedMinutes),
                            expected: minutes(workOrder.expectedMinutes),
                          })}
                        </Table.Td>
                        <Table.Td>{minutes(workOrder.actualMinutes)}</Table.Td>
                        <Table.Td
                          c={
                            deviation === null || workOrder.deviationMinutes === 0
                              ? undefined
                              : workOrder.deviationMinutes > 0
                                ? "red"
                                : "teal"
                          }
                        >
                          {deviation ?? "—"}
                        </Table.Td>
                        <Table.Td>
                          {workOrder.operators.map((operator) => operator.user.displayName).join(", ") || "—"}
                        </Table.Td>
                      </Table.Tr>
                    );
                  })}
                </Table.Tbody>
              </Table>
            </Table.ScrollContainer>
          )}
        </div>
      </PageSection>
      <PageSection
        title={t("timeLogs")}
        actions={
          editable &&
          (workOrders.data?.length ?? 0) > 0 && (
            <Button
              variant="light"
              size="xs"
              leftSection={<IconPlus size={14} />}
              data-assist="manufacturing.order.add-log"
              onClick={() => setAdding(true)}
            >
              {t("addMissingLog")}
            </Button>
          )
        }
      >
        <div data-assist="manufacturing.order.time-logs">
          {logs.error ? (
            <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
              {logs.error.message}
            </Alert>
          ) : logs.isPending ? (
            <ContentSkeleton rows={2} />
          ) : logs.data.length === 0 ? (
            <Text size="sm" c="dimmed">
              {t("noTimeLogs")}
            </Text>
          ) : (
            <Table.ScrollContainer minWidth={900}>
              <Table>
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>{t("person")}</Table.Th>
                    <Table.Th>{t("workOrder")}</Table.Th>
                    <Table.Th>{t("logKind")}</Table.Th>
                    <Table.Th>{t("startedAt")}</Table.Th>
                    <Table.Th>{t("endedAt")}</Table.Th>
                    <Table.Th>{t("duration")}</Table.Th>
                    <Table.Th>{t("logQuantity")}</Table.Th>
                    <Table.Th>{t("note")}</Table.Th>
                    {editable && <Table.Th />}
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {logs.data.map((log) => {
                    const spent = logMinutes(log);
                    return (
                      <Table.Tr key={log.id}>
                        <Table.Td>{log.user?.displayName ?? t("workCenterLog")}</Table.Td>
                        <Table.Td>{log.workOrderId !== null ? (nameOf.get(log.workOrderId) ?? "—") : "—"}</Table.Td>
                        <Table.Td>
                          <Group gap={4}>
                            {log.kind === "downtime" && log.reason
                              ? t("downtimeOf", { reason: t(`reason_${log.reason}`) })
                              : t(`logKind_${log.kind}`)}
                            {log.forced && (
                              <Badge size="xs" variant="light" color="yellow">
                                {t("forced")}
                              </Badge>
                            )}
                            {log.correctedAt && (
                              <Badge size="xs" variant="light" color="gray">
                                {t("corrected")}
                              </Badge>
                            )}
                            {log.longRunning && (
                              <Badge size="xs" variant="light" color="orange">
                                {t("longRunningBadge")}
                              </Badge>
                            )}
                          </Group>
                        </Table.Td>
                        <Table.Td>{stamp(log.startedAt)}</Table.Td>
                        <Table.Td>{log.endedAt ? stamp(log.endedAt) : t("running")}</Table.Td>
                        <Table.Td>{spent === null ? "—" : minutes(spent)}</Table.Td>
                        <Table.Td>{log.quantity ? decimal(log.quantity) : "—"}</Table.Td>
                        <Table.Td>
                          <Text size="sm" style={{ whiteSpace: "pre-wrap" }}>
                            {log.note || "—"}
                          </Text>
                          {log.correctedBy && log.correctedAt && (
                            <Text size="xs" c="dimmed">
                              {t("correctedBy", { who: log.correctedBy.displayName, when: stamp(log.correctedAt) })}
                            </Text>
                          )}
                        </Table.Td>
                        {editable && (
                          <Table.Td>
                            <Button
                              variant="subtle"
                              size="xs"
                              leftSection={<IconPencil size={14} />}
                              aria-label={t("correctLogOf", { when: stamp(log.startedAt) })}
                              onClick={() => setCorrecting(log)}
                            >
                              {t("correct")}
                            </Button>
                          </Table.Td>
                        )}
                      </Table.Tr>
                    );
                  })}
                </Table.Tbody>
              </Table>
            </Table.ScrollContainer>
          )}
        </div>
      </PageSection>
      <TimeLogDialog
        log={correcting}
        workOrders={workOrders.data ?? []}
        opened={correcting !== undefined}
        onClose={() => setCorrecting(undefined)}
      />
      <TimeLogDialog workOrders={workOrders.data ?? []} opened={adding} onClose={() => setAdding(false)} />
    </Stack>
  );
};
