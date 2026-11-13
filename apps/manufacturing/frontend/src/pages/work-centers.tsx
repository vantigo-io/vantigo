import { Alert, Anchor, Badge, Button, Select, SimpleGrid, Stack, Table, TextInput } from "@mantine/core";
import { IconAlertCircle, IconPlus, IconSearch, IconTool } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader, PageSection } from "@vantigo/frontend-shell";
import { useState } from "react";
import { manufacturingMetaQueryOptions } from "../api/meta";
import { manufacturingSitesQueryOptions } from "../api/sites";
import { statusFilters, workCentersQueryOptions } from "../api/work-centers";
import { WorkCenterGroups } from "../components/work-center-groups";
import { canManageBoms } from "../lib/capabilities";
import { statusColor, useManufacturingFormat } from "../lib/format";
import {
  MANUFACTURING_HOME_ROUTE_PATH,
  NEW_WORK_CENTER_ROUTE_PATH,
  WORK_CENTERS_ROUTE_PATH,
  workCenterLinkOptions,
} from "../lib/routes";
import type { WorkCentersSearch } from "../lib/search";

/**
 * The work centers (M8) by code, filtered by status — active by default —
 * by site and by code or name. The filters live in the URL. New work center
 * needs `manufacturing:boms-manage`.
 */
export const WorkCentersPage = () => {
  const { t, decimal } = useManufacturingFormat();
  const search = useSearch({ strict: false }) as WorkCentersSearch;
  const navigate = useNavigate() as (options: unknown) => Promise<void>;
  const meta = useQuery(manufacturingMetaQueryOptions());
  const sites = useQuery(manufacturingSitesQueryOptions());
  const centers = useQuery(workCentersQueryOptions({ status: search.status, siteId: search.siteId, q: search.q }));
  const [q, setQ] = useState(search.q ?? "");

  const filter = (patch: Partial<WorkCentersSearch>) =>
    void navigate({ to: WORK_CENTERS_ROUTE_PATH, search: { ...search, ...patch }, replace: true });

  return (
    <Stack gap="lg">
      <PageHeader
        breadcrumbs={[{ label: t("manufacturing"), to: MANUFACTURING_HOME_ROUTE_PATH }, { label: t("navWorkCenters") }]}
        title={t("navWorkCenters")}
        description={t("workCentersDescription")}
        actions={
          canManageBoms(meta.data) && (
            <Button
              data-assist="manufacturing.work-centers.new"
              leftSection={<IconPlus size={16} />}
              renderRoot={(props) => <Link to={NEW_WORK_CENTER_ROUTE_PATH} {...props} />}
            >
              {t("newWorkCenter")}
            </Button>
          )
        }
      />
      <PageSection>
        <Stack gap="md" data-assist="manufacturing.work-centers.list">
          <SimpleGrid cols={{ base: 1, sm: 3 }}>
            <Select
              label={t("status")}
              allowDeselect={false}
              data={statusFilters.map((value) => ({ value, label: t(`statusFilter_${value}`) }))}
              value={search.status ?? "active"}
              onChange={(next) =>
                filter({
                  status: next === null || next === "active" ? undefined : (next as WorkCentersSearch["status"]),
                })
              }
            />
            <Select
              label={t("site")}
              placeholder={t("allSites")}
              clearable
              data={(sites.data ?? []).map((site) => ({
                value: String(site.id),
                label: `${site.code} · ${site.name}`,
              }))}
              value={search.siteId === undefined ? null : String(search.siteId)}
              onChange={(next) => filter({ siteId: next === null ? undefined : Number(next) })}
            />
            <TextInput
              label={t("search")}
              placeholder={t("searchWorkCentersPlaceholder")}
              leftSection={<IconSearch size={16} />}
              value={q}
              onChange={(event) => setQ(event.currentTarget.value)}
              onBlur={() => filter({ q: q.trim() || undefined })}
              onKeyDown={(event) => {
                if (event.key !== "Enter") return;
                event.preventDefault();
                filter({ q: q.trim() || undefined });
              }}
            />
          </SimpleGrid>
          {centers.error ? (
            <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
              {centers.error.message}
            </Alert>
          ) : centers.isPending ? (
            <ContentSkeleton rows={3} />
          ) : centers.data.length === 0 ? (
            <EmptyState
              icon={IconTool}
              title={t("noWorkCenters")}
              description={canManageBoms(meta.data) ? t("noWorkCentersHint") : undefined}
            />
          ) : (
            <Table.ScrollContainer minWidth={640}>
              <Table highlightOnHover>
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>{t("code")}</Table.Th>
                    <Table.Th>{t("name")}</Table.Th>
                    <Table.Th>{t("site")}</Table.Th>
                    <Table.Th>{t("capacity")}</Table.Th>
                    <Table.Th>{t("timeEfficiency")}</Table.Th>
                    <Table.Th>{t("status")}</Table.Th>
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {centers.data.map((center) => (
                    <Table.Tr key={center.id}>
                      <Table.Td>
                        <Anchor renderRoot={(props) => <Link {...workCenterLinkOptions(center.id)} {...props} />}>
                          {center.code}
                        </Anchor>
                      </Table.Td>
                      <Table.Td>{center.name}</Table.Td>
                      <Table.Td>{center.siteCode}</Table.Td>
                      <Table.Td>{decimal(String(center.capacity))}</Table.Td>
                      <Table.Td>{t("percentValue", { value: center.timeEfficiency })}</Table.Td>
                      <Table.Td>
                        <Badge variant="light" color={statusColor[center.status]}>
                          {t(`status_${center.status}`)}
                        </Badge>
                        {center.workingState === "blocked" && (
                          <Badge ml="xs" variant="light" color="red">
                            {t("workingState_blocked")}
                          </Badge>
                        )}
                      </Table.Td>
                    </Table.Tr>
                  ))}
                </Table.Tbody>
              </Table>
            </Table.ScrollContainer>
          )}
        </Stack>
      </PageSection>
      <WorkCenterGroups />
    </Stack>
  );
};
