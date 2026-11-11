import { Alert, Button, Group, SimpleGrid, Stack } from "@mantine/core";
import { IconAlertCircle, IconListDetails, IconPlus } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, KpiCard, PageHeader } from "@vantigo/frontend-shell";
import { manufacturingMetaQueryOptions } from "../api/meta";
import { manufacturingStatsQueryOptions } from "../api/stats";
import { canManageBoms } from "../lib/capabilities";
import { useManufacturingFormat } from "../lib/format";
import { BOMS_ROUTE_PATH, NEW_BOM_ROUTE_PATH, WORK_CENTERS_ROUTE_PATH } from "../lib/routes";

/**
 * The app's home (M7). Delivery A has work centers and BoMs, so the cards
 * are theirs from `GET /stats`: active work centers, the blocked among them,
 * and active BoMs. The manufacturing orders' cards — to start today, in
 * progress, late — and the late list join with delivery B. With no BoM yet
 * the page points to Bills of materials instead.
 */
export const ManufacturingOverviewPage = () => {
  const { t } = useManufacturingFormat();
  const meta = useQuery(manufacturingMetaQueryOptions());
  const stats = useQuery(manufacturingStatsQueryOptions());

  return (
    <Stack gap="lg">
      <PageHeader
        title={t("overviewTitle")}
        description={t("overviewDescription")}
        actions={
          canManageBoms(meta.data) && (
            <Group gap="xs">
              <Button
                data-assist="manufacturing.overview.new-bom"
                leftSection={<IconPlus size={16} />}
                renderRoot={(props) => <Link to={NEW_BOM_ROUTE_PATH} {...props} />}
              >
                {t("newBom")}
              </Button>
            </Group>
          )
        }
      />
      {stats.error ? (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
          {stats.error.message}
        </Alert>
      ) : stats.isPending ? (
        <ContentSkeleton rows={1} rowHeight={150} />
      ) : (
        <>
          <SimpleGrid cols={{ base: 1, xs: 3 }} spacing="md" data-assist="manufacturing.overview.cards">
            <KpiCard
              label={t("activeWorkCenters")}
              value={stats.data.activeWorkCenters}
              href={WORK_CENTERS_ROUTE_PATH}
            />
            <KpiCard
              label={t("blockedWorkCenters")}
              value={stats.data.blockedWorkCenters}
              href={WORK_CENTERS_ROUTE_PATH}
            />
            <KpiCard label={t("activeBoms")} value={stats.data.activeBoms} href={BOMS_ROUTE_PATH} />
          </SimpleGrid>
          {stats.data.activeBoms === 0 && (
            <EmptyState
              icon={IconListDetails}
              title={t("noBomsYet")}
              description={t("noBomsYetHint")}
              action={
                <Button variant="light" renderRoot={(props) => <Link to={BOMS_ROUTE_PATH} {...props} />}>
                  {t("goToBoms")}
                </Button>
              }
            />
          )}
        </>
      )}
    </Stack>
  );
};
