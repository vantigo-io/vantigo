import { Alert, Badge, Button, Card, Group, SimpleGrid, Stack, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconAlertTriangle, IconLockOpen, IconPlayerPause, IconUser } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader, PageSection } from "@vantigo/frontend-shell";
import { useEffect, useState } from "react";
import { manufacturingMetaQueryOptions } from "../api/meta";
import { NotFoundError } from "../api/request";
import { unblockWorkCenter, useWorkCenterLive, workCenterQueueQueryOptions } from "../api/shop-floor";
import { manufacturingSitesQueryOptions } from "../api/sites";
import { workCentersQueryOptions } from "../api/work-centers";
import { BlockDialog } from "../components/block-dialog";
import { WorkOrderCard } from "../components/work-order-card";
import { canWorkOnShopFloor } from "../lib/capabilities";
import { refusalMessage, useInvalidateManufacturing, useManufacturingFormat } from "../lib/format";
import { MY_WORK_ROUTE_PATH, shopFloorWorkCenterLinkOptions } from "../lib/routes";
import { shopFloorBreadcrumbs, TOUCH_CARD_MIN_HEIGHT } from "../lib/shop-floor";
import { readRememberedWorkCenter, rememberWorkCenter } from "../lib/shop-floor-storage";

/** The button to My work, on every shop-floor screen but My work. */
const MyWorkButton = () => {
  const { t } = useManufacturingFormat();
  return (
    <Button
      size="lg"
      variant="default"
      leftSection={<IconUser size={20} />}
      renderRoot={(props) => <Link to={MY_WORK_ROUTE_PATH} {...props} />}
    >
      {t("myWork")}
    </Button>
  );
};

/**
 * The shop floor (M31): choose a work center. The one this device last worked
 * at is offered first — remembered in the browser as a convenience only, so
 * the picker is the same without it.
 */
export const ShopFloorPage = () => {
  const { t } = useManufacturingFormat();
  const centers = useQuery(workCentersQueryOptions());
  const sites = useQuery(manufacturingSitesQueryOptions());
  const [rememberedId] = useState(readRememberedWorkCenter);
  const remembered = centers.data?.find((center) => center.id === rememberedId);
  const siteOf = (siteId: number) => sites.data?.find((site) => site.id === siteId);

  return (
    <Stack gap="lg">
      <PageHeader
        breadcrumbs={shopFloorBreadcrumbs(t)}
        title={t("navShopFloor")}
        description={t("shopFloorDescription")}
        actions={<MyWorkButton />}
      />
      {centers.error ? (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
          {centers.error.message}
        </Alert>
      ) : centers.isPending ? (
        <ContentSkeleton rows={3} rowHeight={TOUCH_CARD_MIN_HEIGHT} />
      ) : centers.data.length === 0 ? (
        <EmptyState title={t("noWorkCentersOnFloor")} description={t("noWorkCentersOnFloorHint")} />
      ) : (
        <PageSection title={t("chooseWorkCenter")}>
          <Stack gap="md" data-assist="manufacturing.shop-floor.picker">
            {remembered && (
              <Button
                size="xl"
                renderRoot={(props) => <Link {...shopFloorWorkCenterLinkOptions(remembered.id)} {...props} />}
              >
                {t("continueAt", { name: `${remembered.code} · ${remembered.name}` })}
              </Button>
            )}
            <SimpleGrid cols={{ base: 1, sm: 2, md: 3 }} spacing="md">
              {centers.data.map((center) => {
                const site = siteOf(center.siteId);
                return (
                  <Card
                    key={center.id}
                    withBorder
                    padding="lg"
                    mih={TOUCH_CARD_MIN_HEIGHT}
                    renderRoot={(props) => <Link {...shopFloorWorkCenterLinkOptions(center.id)} {...props} />}
                    aria-label={`${center.code} · ${center.name}`}
                  >
                    <Group justify="space-between" wrap="nowrap">
                      <Text size="xl" fw={700}>
                        {center.code}
                      </Text>
                      {center.workingState === "blocked" && (
                        <Badge size="lg" color="red">
                          {t("workingState_blocked")}
                        </Badge>
                      )}
                    </Group>
                    <Text size="lg">{center.name}</Text>
                    {site && (
                      <Text size="sm" c="dimmed">
                        {site.name}
                      </Text>
                    )}
                  </Card>
                );
              })}
            </SimpleGrid>
          </Stack>
        </PageSection>
      )}
    </Stack>
  );
};

/**
 * A work center on the shop floor (M31): its queue of ready, running and
 * paused work orders, by planned start, priority and due date, each with
 * what remains, when it is due, its components' reservation and who runs it;
 * and Block / Unblock for whoever may work on the floor (M30, M33). It
 * follows `manufacturing.work-center:<id>`, polling only while live is down,
 * and is remembered on the device as the picker's first choice.
 */
export const ShopFloorWorkCenterPage = () => {
  const { t, stamp } = useManufacturingFormat();
  const { workCenterId } = useParams({ strict: false }) as { workCenterId: string };
  const id = Number(workCenterId);
  const live = useWorkCenterLive(id);
  const meta = useQuery(manufacturingMetaQueryOptions());
  const queue = useQuery(workCenterQueueQueryOptions(id, live));
  const invalidate = useInvalidateManufacturing();
  const [blocking, setBlocking] = useState(false);
  const [unblocking, setUnblocking] = useState(false);
  const work = canWorkOnShopFloor(meta.data);
  const data = queue.data;

  useEffect(() => rememberWorkCenter(id), [id]);

  const unblock = async () => {
    setUnblocking(true);
    try {
      await unblockWorkCenter(id);
      notifications.show({ color: "teal", title: t("workCenterUnblocked"), message: "" });
      await invalidate();
    } catch (error) {
      notifications.show({ color: "red", title: t("couldNotUnblock"), message: refusalMessage(t, error as Error) });
    } finally {
      setUnblocking(false);
    }
  };

  const title = data ? `${data.workCenter.code} · ${data.workCenter.name}` : t("workCenter");
  return (
    <Stack gap="lg">
      <PageHeader
        breadcrumbs={shopFloorBreadcrumbs(t, data?.workCenter.code ?? t("workCenter"))}
        title={
          data && data.workCenter.workingState === "blocked" ? (
            <Group gap="sm">
              {title}
              <Badge size="lg" color="red">
                {t("workingState_blocked")}
              </Badge>
            </Group>
          ) : (
            title
          )
        }
        actions={
          <Group gap="sm" data-assist="manufacturing.shop-floor.block">
            <MyWorkButton />
            {data &&
              work &&
              (data.block ? (
                <Button
                  size="lg"
                  color="teal"
                  leftSection={<IconLockOpen size={20} />}
                  loading={unblocking}
                  onClick={() => void unblock()}
                >
                  {t("unblock")}
                </Button>
              ) : (
                <Button
                  size="lg"
                  color="red"
                  leftSection={<IconPlayerPause size={20} />}
                  onClick={() => setBlocking(true)}
                >
                  {t("block")}
                </Button>
              ))}
          </Group>
        }
      />
      {queue.error ? (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
          {queue.error instanceof NotFoundError ? t("workCenterNotFound") : queue.error.message}
        </Alert>
      ) : !data ? (
        <ContentSkeleton rows={3} rowHeight={TOUCH_CARD_MIN_HEIGHT} />
      ) : (
        <>
          {data.block && (
            <Alert
              color="red"
              icon={<IconAlertTriangle size={20} />}
              title={t("blockedBecause", { reason: t(`reason_${data.block.reason}`) })}
            >
              <Stack gap={4}>
                <Text size="md">{t("blockedSince", { when: stamp(data.block.since) })}</Text>
                {data.block.note && (
                  <Text size="md" style={{ whiteSpace: "pre-wrap" }}>
                    {data.block.note}
                  </Text>
                )}
              </Stack>
            </Alert>
          )}
          <PageSection title={t("queue")}>
            <Stack gap="md" data-assist="manufacturing.shop-floor.queue">
              {data.items.length === 0 ? (
                <Text size="lg" c="dimmed">
                  {t("queueEmpty")}
                </Text>
              ) : (
                data.items.map((workOrder) => <WorkOrderCard key={workOrder.id} workOrder={workOrder} />)
              )}
            </Stack>
          </PageSection>
          <BlockDialog workCenter={data.workCenter} opened={blocking} onClose={() => setBlocking(false)} />
        </>
      )}
    </Stack>
  );
};
