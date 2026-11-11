import { Alert, Button, Card, Group, Stack, Text } from "@mantine/core";
import { IconAlertCircle, IconAlertTriangle, IconBuildingFactory2 } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ContentSkeleton, PageHeader, PageSection } from "@vantigo/frontend-shell";
import { type ManufacturingTimeLog, type ManufacturingWorkOrder, myWorkQueryOptions } from "../api/shop-floor";
import { WorkOrderCard } from "../components/work-order-card";
import { useManufacturingFormat } from "../lib/format";
import { SHOP_FLOOR_ROUTE_PATH, workOrderLinkOptions } from "../lib/routes";
import { shopFloorBreadcrumbs, TOUCH_CARD_MIN_HEIGHT } from "../lib/shop-floor";
import { formatElapsed, LONG_RUNNING_MS, useElapsed } from "../lib/timer";

/** The caller's open log, its timer counted on the client, linking to its work order. */
const OpenLog = ({ log, workOrder }: { log: ManufacturingTimeLog; workOrder: ManufacturingWorkOrder | null }) => {
  const { t } = useManufacturingFormat();
  const elapsed = useElapsed(log.startedAt);
  return (
    <Card withBorder padding="lg" mih={TOUCH_CARD_MIN_HEIGHT}>
      <Stack gap="sm">
        <Text size="sm" c="dimmed">
          {log.kind === "downtime" ? t("yourDowntime") : t("yourTime")}
        </Text>
        <Text fz={48} fw={700} ff="monospace" lh={1} role="timer" aria-label={t("yourTime")}>
          {formatElapsed(elapsed)}
        </Text>
        {workOrder && (
          <Text size="lg">{`${workOrder.order.number} · ${workOrder.name} · ${workOrder.workCenter.code}`}</Text>
        )}
        {(log.longRunning || elapsed > LONG_RUNNING_MS) && (
          <Alert color="orange" icon={<IconAlertTriangle size={20} />} title={t("longRunningTitle")}>
            {t("longRunningHint")}
          </Alert>
        )}
        {workOrder && (
          <Group>
            <Button size="lg" renderRoot={(props) => <Link {...workOrderLinkOptions(workOrder.id)} {...props} />}>
              {t("openWorkOrder")}
            </Button>
          </Group>
        )}
      </Stack>
    </Card>
  );
};

/**
 * My work (M31): the caller's one open log, wherever it is, with its timer,
 * and the work orders they logged time on today in the business calendar.
 * The operator is the signed-in user.
 */
export const MyWorkPage = () => {
  const { t } = useManufacturingFormat();
  const mine = useQuery(myWorkQueryOptions());
  return (
    <Stack gap="lg">
      <PageHeader
        breadcrumbs={shopFloorBreadcrumbs(t, t("myWork"))}
        title={t("myWork")}
        description={t("myWorkDescription")}
        actions={
          <Button
            size="lg"
            variant="default"
            leftSection={<IconBuildingFactory2 size={20} />}
            renderRoot={(props) => <Link to={SHOP_FLOOR_ROUTE_PATH} {...props} />}
          >
            {t("chooseWorkCenter")}
          </Button>
        }
      />
      {mine.error ? (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
          {mine.error.message}
        </Alert>
      ) : mine.isPending ? (
        <ContentSkeleton rows={3} rowHeight={TOUCH_CARD_MIN_HEIGHT} />
      ) : (
        <>
          <PageSection title={t("runningNow")}>
            <div data-assist="manufacturing.my-work.open-log">
              {mine.data.openLog ? (
                <OpenLog log={mine.data.openLog} workOrder={mine.data.openWorkOrder} />
              ) : (
                <Text size="lg" c="dimmed">
                  {t("nothingRunning")}
                </Text>
              )}
            </div>
          </PageSection>
          <PageSection title={t("today")}>
            <Stack gap="md">
              {mine.data.today.length === 0 ? (
                <Text size="lg" c="dimmed">
                  {t("nothingToday")}
                </Text>
              ) : (
                mine.data.today.map((workOrder) => <WorkOrderCard key={workOrder.id} workOrder={workOrder} />)
              )}
            </Stack>
          </PageSection>
        </>
      )}
    </Stack>
  );
};
