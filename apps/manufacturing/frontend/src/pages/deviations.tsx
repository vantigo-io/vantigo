import { Alert, Anchor, Badge, Button, Group, Pagination, Select, Stack, Table, Text, TextInput } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconDownload, IconZoomExclamation } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader } from "@vantigo/frontend-shell";
import { useState } from "react";
import { manufacturingMetaQueryOptions } from "../api/meta";
import {
  DEVIATION_PAGE_SIZE,
  type DeviationFilter,
  deviationStatuses,
  deviationsQueryOptions,
  downloadDeviationsCsv,
  type ManufacturingDeviationStatus,
  useDeviationsLive,
} from "../api/quality";
import { saveCsv } from "../api/reports";
import { DeviationDialog } from "../components/deviation-dialog";
import { canRecordQuality } from "../lib/capabilities";
import { refusalMessage, useManufacturingFormat } from "../lib/format";
import { deviationLots, deviationStatusColor } from "../lib/quality";
import { deviationLinkOptions, MANUFACTURING_HOME_ROUTE_PATH, orderLinkOptions } from "../lib/routes";

/**
 * The deviations list (manufacturing phase 2 M99; nb Avvik), under the
 * overview: newest first, filtered by status, order, product, lot and the
 * period they were opened in — the order and lot as a link from their pages
 * sets them (`?orderId=`, `?lotId=`) — with every match as CSV. Read again on
 * the deviations' live topic.
 */
export const DeviationsPage = () => {
  const { t, stamp } = useManufacturingFormat();
  const search = useSearch({ strict: false }) as { orderId?: number; lotId?: number; variantId?: number; new?: number };
  const navigate = useNavigate() as (options: unknown) => Promise<void>;
  const meta = useQuery(manufacturingMetaQueryOptions());
  const [reporting, setReporting] = useState(search.new === 1);
  useDeviationsLive();
  const [status, setStatus] = useState<ManufacturingDeviationStatus | undefined>("open");
  const [from, setFrom] = useState<string>();
  const [to, setTo] = useState<string>();
  const [page, setPage] = useState(1);
  const [downloading, setDownloading] = useState(false);
  const filter: DeviationFilter = {
    status,
    orderId: search.orderId,
    lotId: search.lotId,
    variantId: search.variantId,
    from,
    to,
    offset: (page - 1) * DEVIATION_PAGE_SIZE,
  };
  const list = useQuery(deviationsQueryOptions(filter));

  const download = async () => {
    setDownloading(true);
    try {
      saveCsv(await downloadDeviationsCsv(filter));
    } catch (error) {
      notifications.show({ color: "red", title: t("couldNotDownload"), message: refusalMessage(t, error as Error) });
    } finally {
      setDownloading(false);
    }
  };

  return (
    <Stack gap="lg">
      <PageHeader
        breadcrumbs={[
          { label: t("manufacturing"), to: MANUFACTURING_HOME_ROUTE_PATH },
          { label: t("navOverview"), to: MANUFACTURING_HOME_ROUTE_PATH },
          { label: t("deviations") },
        ]}
        title={t("deviations")}
        description={t("deviationsDescription")}
        actions={
          <Group gap="xs">
            {canRecordQuality(meta.data) && (
              <Button
                color="red"
                leftSection={<IconZoomExclamation size={16} />}
                data-assist="manufacturing.deviations.report"
                onClick={() => setReporting(true)}
              >
                {t("reportDeviation")}
              </Button>
            )}
            <Button
              variant="default"
              leftSection={<IconDownload size={16} />}
              loading={downloading}
              onClick={() => void download()}
            >
              {t("downloadCsv")}
            </Button>
          </Group>
        }
      />
      <DeviationDialog
        opened={reporting}
        onClose={() => setReporting(false)}
        orderId={search.orderId}
        lotId={search.lotId}
        onCreated={(deviation) => void navigate(deviationLinkOptions(deviation.id))}
      />
      <Group gap="sm" align="flex-end" data-assist="manufacturing.deviations.filters">
        <Select
          label={t("status")}
          placeholder={t("allStatuses")}
          clearable
          w={180}
          data={deviationStatuses.map((value) => ({ value, label: t(`deviationStatus_${value}`) }))}
          value={status ?? null}
          onChange={(next) => {
            setStatus((next as ManufacturingDeviationStatus | null) ?? undefined);
            setPage(1);
          }}
        />
        <TextInput
          type="date"
          label={t("reportFrom")}
          value={from ?? ""}
          onChange={(event) => setFrom(event.currentTarget.value || undefined)}
        />
        <TextInput
          type="date"
          label={t("reportTo")}
          value={to ?? ""}
          onChange={(event) => setTo(event.currentTarget.value || undefined)}
        />
      </Group>
      {list.error ? (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
          {list.error.message}
        </Alert>
      ) : list.isPending ? (
        <ContentSkeleton rows={3} />
      ) : list.data.items.length === 0 ? (
        <EmptyState icon={IconAlertCircle} title={t("noDeviations")} description={t("noDeviationsHint")} />
      ) : (
        <Stack gap="sm">
          <Table.ScrollContainer minWidth={860}>
            <Table data-assist="manufacturing.deviations.list">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("deviationNumber")}</Table.Th>
                  <Table.Th>{t("deviationTitle")}</Table.Th>
                  <Table.Th>{t("status")}</Table.Th>
                  <Table.Th>{t("order")}</Table.Th>
                  <Table.Th>{t("variant")}</Table.Th>
                  <Table.Th>{t("lots")}</Table.Th>
                  <Table.Th>{t("deviationOpenedAt")}</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {list.data.items.map((deviation) => (
                  <Table.Tr key={deviation.id}>
                    <Table.Td>
                      <Anchor
                        size="sm"
                        renderRoot={(props) => <Link {...deviationLinkOptions(deviation.id)} {...props} />}
                      >
                        {deviation.number}
                      </Anchor>
                    </Table.Td>
                    <Table.Td>
                      <Group gap={6}>
                        <Text size="sm">{deviation.title}</Text>
                        {deviation.quarantine && (
                          <Badge size="sm" variant="light" color="orange">
                            {t("quarantine")}
                          </Badge>
                        )}
                      </Group>
                    </Table.Td>
                    <Table.Td>
                      <Badge variant="light" color={deviationStatusColor[deviation.status]}>
                        {t(`deviationStatus_${deviation.status}`)}
                      </Badge>
                    </Table.Td>
                    <Table.Td>
                      {deviation.order ? (
                        <Anchor
                          size="sm"
                          renderRoot={(props) => <Link {...orderLinkOptions(deviation.order?.id ?? 0)} {...props} />}
                        >
                          {deviation.order.number}
                        </Anchor>
                      ) : (
                        "—"
                      )}
                    </Table.Td>
                    <Table.Td>{deviation.variant?.sku ?? "—"}</Table.Td>
                    <Table.Td>{deviationLots(deviation) || "—"}</Table.Td>
                    <Table.Td>{stamp(deviation.openedAt)}</Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
          {list.data.total > DEVIATION_PAGE_SIZE && (
            <Pagination value={page} onChange={setPage} total={Math.ceil(list.data.total / DEVIATION_PAGE_SIZE)} />
          )}
        </Stack>
      )}
    </Stack>
  );
};
