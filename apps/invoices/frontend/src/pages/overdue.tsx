import {
  Alert,
  Badge,
  Button,
  Card,
  Checkbox,
  Group,
  Pagination,
  Select,
  Stack,
  Table,
  Text,
  Title,
} from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { IconAlertCircle, IconMail, IconPrinter } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { ContentSkeleton, PageHeader } from "@vantigo/frontend-shell";
import { useState } from "react";
import { invoicesMetaQueryOptions } from "../api/meta";
import {
  OVERDUE_ACTIONS,
  type OverdueAction,
  type OverdueFilters,
  type OverdueItem,
  overdueQueryOptions,
  reminderRunsQueryOptions,
} from "../api/overdue";
import { CustomerPicker } from "../components/customer-picker";
import { DocumentLink, RouteLink } from "../components/document-link";
import "../i18n";
import { useWho } from "../lib/bank";
import { refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";
import { letterName, useWords } from "../lib/reminders";
import { REMINDER_PRINT_ROUTE_PATH, reminderRunHref, reminderRunLinkOptions } from "../lib/routes";
import { FreshnessAlert, ListWarnings, RunPreviewModal } from "./-run-preview-modal";

export interface OverduePageProps {
  /** Whether the caller holds `customers:view`, which the customer filter's picker reads with. */
  canViewCustomers: boolean;
  /** The signed-in user's id, so a run of theirs says "you". */
  currentUserId?: string;
}

/**
 * The Overdue area (invoices payments and reminders design D12, D22): every
 * overdue invoice judged by the reminder engine today — what comes next and
 * why — filtered by customer, next action and due date, with the bank data's
 * freshness and the OCR note above it. Reading it needs `invoices:access`;
 * meta's `canRunReminders` (`invoices:payments`) adds "Send reminders", the
 * paper letters and the runs.
 */
export const OverduePage = ({ canViewCustomers, currentUserId }: OverduePageProps) => {
  const { t, date } = useInvoiceFormat();
  const meta = useQuery(invoicesMetaQueryOptions());
  const [customerId, setCustomerId] = useState<number | null>(null);
  const [dueBefore, setDueBefore] = useState<string | null>(null);
  const [action, setAction] = useState<OverdueAction | "">("");
  const [chargesOutstanding, setChargesOutstanding] = useState(false);
  const [page, setPage] = useState(1);
  const [previewing, setPreviewing] = useState(false);
  const filters: OverdueFilters = {
    ...(customerId !== null ? { customerId } : {}),
    ...(dueBefore ? { dueBefore } : {}),
    ...(action ? { action } : {}),
    ...(chargesOutstanding ? { charges: "outstanding" as const } : {}),
    page,
  };
  const list = useQuery(overdueQueryOptions(filters));
  const canRun = Boolean(meta.data?.capabilities.canRunReminders);
  const currency = meta.data?.currency ?? "NOK";
  return (
    <Stack gap="lg">
      <PageHeader
        title={t("overdue.title")}
        description={t("overdue.description")}
        actions={
          canRun ? (
            <Group gap="sm">
              <RouteLink href={REMINDER_PRINT_ROUTE_PATH} to={{ to: REMINDER_PRINT_ROUTE_PATH }}>
                <Group gap={4} component="span">
                  <IconPrinter size={16} />
                  {t("overdue.paperLetters")}
                </Group>
              </RouteLink>
              <Button leftSection={<IconMail size={16} />} onClick={() => setPreviewing(true)}>
                {t("overdue.sendReminders")}
              </Button>
            </Group>
          ) : undefined
        }
      />
      {meta.isError && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadMeta")}>
          {refusalMessage(meta.error, t, date)}
        </Alert>
      )}
      {list.data && (
        <>
          <FreshnessAlert freshness={list.data.freshness} />
          <ListWarnings warnings={list.data.warnings} />
        </>
      )}
      <Group align="flex-end">
        {canViewCustomers && (
          <CustomerPicker
            label={t("overdue.filter.customer")}
            anyStatus
            clearable
            value={customerId}
            onChange={(id) => {
              setCustomerId(id);
              setPage(1);
            }}
          />
        )}
        <Select
          label={t("overdue.filter.action")}
          data={[
            { value: "", label: t("overdue.filter.anyAction") },
            ...OVERDUE_ACTIONS.map((a) => ({ value: a, label: t(`overdue.action.${a}`) })),
          ]}
          value={action}
          allowDeselect={false}
          onChange={(value) => {
            setAction((value ?? "") as OverdueAction | "");
            setPage(1);
          }}
        />
        <DateInput
          label={t("overdue.filter.dueBefore")}
          clearable
          valueFormat={t("dateInputFormat")}
          value={dueBefore}
          onChange={(d) => {
            setDueBefore(d);
            setPage(1);
          }}
        />
        <Checkbox
          label={t("overdue.filter.chargesOutstanding")}
          checked={chargesOutstanding}
          onChange={(event) => {
            setChargesOutstanding(event.currentTarget.checked);
            setPage(1);
          }}
        />
      </Group>
      {list.isError && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("overdue.failedToLoad")}>
          {refusalMessage(list.error, t, date)}
        </Alert>
      )}
      {list.isPending && <ContentSkeleton rows={4} rowHeight={40} />}
      {list.data && list.data.items.length === 0 && (
        <Text size="sm" c="dimmed">
          {t("overdue.none")}
        </Text>
      )}
      {list.data && list.data.items.length > 0 && (
        <>
          <OverdueTable items={list.data.items} currency={currency} />
          <Text size="xs" c="dimmed">
            {t("overdue.total", { count: list.data.total })}
          </Text>
        </>
      )}
      {list.data && list.data.total > 25 && (
        <Pagination total={Math.ceil(list.data.total / 25)} value={page} onChange={setPage} />
      )}
      {canRun && <RunsCard currentUserId={currentUserId} />}
      {previewing && (
        <RunPreviewModal
          scope={{ ...(customerId !== null ? { customerId } : {}), ...(dueBefore ? { dueBefore } : {}) }}
          currency={currency}
          onClose={() => setPreviewing(false)}
        />
      )}
    </Stack>
  );
};

/** The overdue invoices: each with its figures, its last letter and what comes next, and why. */
const OverdueTable = ({ items, currency }: { items: OverdueItem[]; currency: string }) => {
  const { t, money, date } = useInvoiceFormat();
  const words = useWords();
  return (
    <Table.ScrollContainer minWidth={1100}>
      <Table>
        <Table.Thead>
          <Table.Tr>
            <Table.Th>{t("overdue.col.invoice")}</Table.Th>
            <Table.Th>{t("overdue.col.customer")}</Table.Th>
            <Table.Th>{t("overdue.col.dueDate")}</Table.Th>
            <Table.Th ta="right">{t("overdue.col.daysOverdue")}</Table.Th>
            <Table.Th ta="right">{t("overdue.col.principalOpen")}</Table.Th>
            <Table.Th ta="right">{t("overdue.col.charges")}</Table.Th>
            <Table.Th ta="right">{t("overdue.col.interestToday")}</Table.Th>
            <Table.Th>{t("overdue.col.lastLetter")}</Table.Th>
            <Table.Th>{t("overdue.col.next")}</Table.Th>
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {items.map((item) => {
            const next = item.nextAction;
            return (
              <Table.Tr key={item.invoiceId}>
                <Table.Td>
                  <DocumentLink invoiceId={item.invoiceId}>{String(item.number)}</DocumentLink>
                </Table.Td>
                <Table.Td>
                  <Text size="sm">{item.buyerName}</Text>
                  <Group gap={4}>
                    {item.hold && (
                      <Badge size="xs" color="orange" variant="light">
                        {t("overdue.onHold")}
                      </Badge>
                    )}
                    {item.handoff && (
                      <Badge size="xs" color="grape" variant="light">
                        {t("overdue.handedOff")}
                      </Badge>
                    )}
                    {item.policyMode !== "normal" && (
                      <Badge size="xs" color="gray" variant="light">
                        {words(`overdue.policy.${item.policyMode}`, item.policyMode)}
                      </Badge>
                    )}
                    {!item.delivered && (
                      <Badge size="xs" color="red" variant="light">
                        {t("overdue.notDelivered")}
                      </Badge>
                    )}
                  </Group>
                </Table.Td>
                <Table.Td>{date(item.dueDate)}</Table.Td>
                <Table.Td ta="right">{item.daysOverdue}</Table.Td>
                <Table.Td ta="right">{money(item.principalOpen, currency)}</Table.Td>
                <Table.Td ta="right">
                  {item.charges.outstanding !== 0 ? money(item.charges.outstanding, currency) : ""}
                </Table.Td>
                <Table.Td ta="right">
                  {item.interestToday !== undefined ? money(item.interestToday, currency) : ""}
                </Table.Td>
                <Table.Td>
                  {item.lastLetter && (
                    <Text size="sm">
                      {t("overdue.lastLetter", {
                        letter: letterName(t, item.lastLetter.sequence),
                        status: words(`reminder.status.${item.lastLetter.status}`, item.lastLetter.status),
                      })}
                    </Text>
                  )}
                </Table.Td>
                <Table.Td>
                  <Text size="sm" fw={600}>
                    {words(`overdue.action.${next.action}`, next.action)}
                    {next.earliestOn &&
                      ` ${t(next.action === "waiting" ? "overdue.until" : "overdue.from", { date: date(next.earliestOn) })}`}
                  </Text>
                  {next.reasons.map((reason) => (
                    <Text key={reason} size="xs" c="dimmed">
                      {words(`reminder.reason.${reason}`, reason, {
                        kind: next.outdated ? words(`rates.kind.${next.outdated.kind}`, next.outdated.kind) : "",
                        halfYear: next.outdated?.halfYear ?? "",
                      })}
                    </Text>
                  ))}
                </Table.Td>
              </Table.Tr>
            );
          })}
        </Table.Tbody>
      </Table>
    </Table.ScrollContainer>
  );
};

/** The reminder runs, newest first, each linking to its page (`invoices:payments`). */
const RunsCard = ({ currentUserId }: { currentUserId?: string }) => {
  const { t, date, dateTime } = useInvoiceFormat();
  const who = useWho(currentUserId);
  const [page, setPage] = useState(1);
  const runs = useQuery(reminderRunsQueryOptions(page));
  return (
    <Card withBorder data-testid="reminder-runs">
      <Stack gap="sm">
        <Title order={4}>{t("overdue.runs")}</Title>
        {runs.isError && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("overdue.failedToLoadRuns")}>
            {refusalMessage(runs.error, t, date)}
          </Alert>
        )}
        {runs.isPending && <ContentSkeleton rows={2} rowHeight={32} />}
        {runs.data && runs.data.data.length === 0 && (
          <Text size="sm" c="dimmed">
            {t("overdue.noRuns")}
          </Text>
        )}
        {runs.data && runs.data.data.length > 0 && (
          <Table>
            <Table.Tbody>
              {runs.data.data.map((run) => (
                <Table.Tr key={run.id}>
                  <Table.Td>
                    <RouteLink href={reminderRunHref(run.id)} to={reminderRunLinkOptions(run.id)}>
                      {t("overdue.runName", { id: run.id })}
                    </RouteLink>
                  </Table.Td>
                  <Table.Td>
                    {dateTime(run.createdAt)}, {who(run.createdBy)}
                  </Table.Td>
                  <Table.Td>
                    {run.letters !== undefined && run.skipped !== undefined
                      ? t("overdue.runCounts", { count: run.letters, skipped: run.skipped })
                      : t("overdue.runUnfinished")}
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        )}
        {runs.data && runs.data.pagination.totalPages > 1 && (
          <Pagination total={runs.data.pagination.totalPages} value={page} onChange={setPage} />
        )}
      </Stack>
    </Card>
  );
};
