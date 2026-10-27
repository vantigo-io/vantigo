import { Alert, Badge, Button, Card, Group, Pagination, Stack, Table, Tabs, Text } from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconArrowLeft } from "@tabler/icons-react";
import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, PageHeader } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  BILLING_ITEM_STATUSES,
  type BillingRunItem,
  billingRunQueryOptions,
  cancelBillingRun,
  runIsLive,
} from "../api/billing-runs";
import { invoicesMetaQueryOptions } from "../api/meta";
import { INVOICES_QUERY_KEY } from "../api/request";
import { BillingRunCountBadges } from "../components/billing-run-counts";
import { DocumentLink, RouteLink } from "../components/document-link";
import "../i18n";
import {
  BILLING_RUN_POLL_MS,
  itemCodeWords,
  itemStatusColour,
  itemStatusWords,
  runStatusColour,
  runStatusWords,
} from "../lib/billing-runs";
import { refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";
import { BILLING_RUNS_ROUTE_PATH } from "../lib/routes";

export interface BillingRunPageProps {
  runId: number;
}

const ALL = "all";

/**
 * One billing run (energy consumption billing design D10, plan reading 11):
 * its through date, issue or draft only, status and times, its items counted
 * by status, **Cancel** while it is queued or running, and its items — one per
 * customer — filtered by status a page at a time, each with its outcome, the
 * documents it made and why it was skipped or failed. The page reads the run
 * again every few seconds until it is done or cancelled.
 */
export const BillingRunPage = ({ runId }: BillingRunPageProps) => {
  const { t, date, dateTime } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const meta = useQuery(invoicesMetaQueryOptions());
  const [status, setStatus] = useState<string>(ALL);
  const [page, setPage] = useState(1);
  const detail = useQuery({
    ...billingRunQueryOptions(runId, status === ALL ? null : status, page),
    placeholderData: keepPreviousData,
    refetchInterval: (query) => {
      const run = query.state.data?.run;
      return run && runIsLive(run) ? BILLING_RUN_POLL_MS : false;
    },
  });
  const cancel = useMutation({
    mutationFn: () => cancelBillingRun(runId),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      notifications.show({ color: "green", message: t("billingRun.cancelledDone") });
    },
    onError: async (error) => {
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY, "billing-run", runId] });
      notifications.show({
        color: "red",
        title: t("billingRun.couldNotCancel"),
        message: refusalMessage(error, t, date),
      });
    },
  });
  const confirmCancel = () =>
    modals.openConfirmModal({
      title: t("billingRun.cancelTitle", { id: runId }),
      children: <Text size="sm">{t("billingRun.cancelBody")}</Text>,
      labels: { confirm: t("billingRun.cancel"), cancel: t("billingRun.keepRunning") },
      confirmProps: { color: "red" },
      onConfirm: () => cancel.mutate(),
    });

  const run = detail.data?.run;
  const canCancel = run !== undefined && runIsLive(run) && (meta.data?.capabilities.canCreate ?? false);

  return (
    <Stack gap="lg">
      <RouteLink href={BILLING_RUNS_ROUTE_PATH} to={{ to: BILLING_RUNS_ROUTE_PATH }}>
        <Group gap={4} component="span">
          <IconArrowLeft size={14} />
          {t("billingRun.backToRuns")}
        </Group>
      </RouteLink>
      <PageHeader
        title={t("billingRun.name", { id: runId })}
        actions={
          canCancel ? (
            <Button color="red" variant="light" loading={cancel.isPending} onClick={confirmCancel}>
              {t("billingRun.cancel")}
            </Button>
          ) : undefined
        }
      />
      {detail.isPending && <ContentSkeleton rows={4} rowHeight={40} />}
      {detail.isError && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("billingRun.failedToLoad")}>
          {refusalMessage(detail.error, t, date)}
        </Alert>
      )}
      {run && detail.data && (
        <>
          <Card withBorder>
            <Stack gap={4}>
              <Group gap="xs">
                <Badge variant="light" color={runStatusColour[run.status] ?? "gray"} data-testid="billing-run-status">
                  {runStatusWords(run.status, t)}
                </Badge>
              </Group>
              <Text size="sm">{t("billingRun.throughLine", { date: date(run.through) })}</Text>
              <Text size="sm">{run.issue ? t("billingRun.modeLine.issue") : t("billingRun.modeLine.draft")}</Text>
              <Text size="sm">{t("billingRun.createdLine", { at: dateTime(run.createdAt) })}</Text>
              <Text size="sm">
                {run.startedAt
                  ? t("billingRun.startedLine", { at: dateTime(run.startedAt) })
                  : t("billingRun.notStarted")}
              </Text>
              {run.finishedAt && (
                <Text size="sm">{t("billingRun.finishedLine", { at: dateTime(run.finishedAt) })}</Text>
              )}
              <BillingRunCountBadges counts={run.counts} all />
            </Stack>
          </Card>
          <Tabs
            value={status}
            onChange={(next) => {
              setStatus(next ?? ALL);
              setPage(1);
            }}
          >
            <Tabs.List>
              <Tabs.Tab value={ALL}>{t("billingRun.filterAll")}</Tabs.Tab>
              {BILLING_ITEM_STATUSES.map((s) => (
                <Tabs.Tab key={s} value={s}>
                  {itemStatusWords(s, t)}
                </Tabs.Tab>
              ))}
            </Tabs.List>
          </Tabs>
          {detail.data.items.length === 0 ? (
            <Text size="sm" c="dimmed">
              {t("billingRun.noItems")}
            </Text>
          ) : (
            <Table.ScrollContainer minWidth={720}>
              <Table data-testid="billing-run-items">
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>{t("billingRun.col.customer")}</Table.Th>
                    <Table.Th>{t("billingRun.col.outcome")}</Table.Th>
                    <Table.Th>{t("billingRun.col.documents")}</Table.Th>
                    <Table.Th>{t("billingRun.col.reason")}</Table.Th>
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {detail.data.items.map((item) => (
                    <ItemRow key={item.id} item={item} />
                  ))}
                </Table.Tbody>
              </Table>
            </Table.ScrollContainer>
          )}
          {detail.data.pagination.totalPages > 1 && (
            <Pagination total={detail.data.pagination.totalPages} value={page} onChange={setPage} />
          )}
        </>
      )}
    </Stack>
  );
};

const ItemRow = ({ item }: { item: BillingRunItem }) => {
  const { t } = useInvoiceFormat();
  return (
    <Table.Tr data-testid={`billing-run-item-${item.id}`}>
      <Table.Td>{item.customerName ?? t("billingRun.customerById", { id: item.customerId })}</Table.Td>
      <Table.Td>
        <Badge variant="light" color={itemStatusColour[item.status] ?? "gray"}>
          {itemStatusWords(item.status, t)}
        </Badge>
      </Table.Td>
      <Table.Td>
        <Group gap="xs">
          {item.invoices.map((doc) => (
            <DocumentLink key={doc.invoiceId} invoiceId={doc.invoiceId}>
              {doc.number !== undefined
                ? t("billingRun.documentNumber", { number: doc.number })
                : t("billingRun.documentDraft")}
            </DocumentLink>
          ))}
        </Group>
      </Table.Td>
      <Table.Td>
        {item.code && (
          <Stack gap={2}>
            <Text size="sm">{itemCodeWords(item.code, t)}</Text>
            {item.detail && (
              <Text size="xs" c="dimmed">
                {item.detail}
              </Text>
            )}
          </Stack>
        )}
      </Table.Td>
    </Table.Tr>
  );
};
