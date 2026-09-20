import {
  Alert,
  Button,
  Card,
  Checkbox,
  Group,
  Pagination,
  SegmentedControl,
  Stack,
  Table,
  Text,
  Title,
} from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconCash, IconDownload, IconLock } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import type { Expense } from "../api/entries";
import {
  downloadReimbursementsCsv,
  type ExpenseReimbursementGroup,
  expenseReimbursementsQueryOptions,
  saveCsv,
  undoExpensesReimbursed,
} from "../api/reimbursements";
import type { ApiError } from "../api/request";
import { CurrencyTotals } from "../components/currency-totals";
import { RefusalList } from "../components/refusal-list";
import "../i18n";
import { refusalMessage, refusalsByEntry } from "../lib/errors";
import { useExpenseFormat } from "../lib/format";
import type { ReimbursementsSearch } from "../lib/search";
import { MarkReimbursedModal } from "./-mark-reimbursed-modal";

export type { ReimbursementsSearch } from "../lib/search";

/**
 * Reimbursements (design §7): what a payroll run is made from, grouped per
 * person with their totals per currency, and — on the other half of the
 * switch — what has already been paid, where an undo is reachable.
 *
 * The page needs nothing from the host; the endpoint is `expenses:manage`
 * only, and a caller without it gets the 403 this shows as an empty state.
 */
export const ReimbursementsPage = () => {
  const { t } = useI18n("expenses");
  const queryClient = useQueryClient();
  const search = useSearch({ strict: false }) as ReimbursementsSearch;
  const navigate = useNavigate() as (options: unknown) => void;
  const { state = "waiting", page = 1, from, to } = search;

  const [selected, setSelected] = useState<number[]>([]);
  const [refusals, setRefusals] = useState<Map<number, string[]>>(new Map());
  const [exportError, setExportError] = useState<string[]>([]);
  const [marking, setMarking] = useState<number[] | null>(null);

  const [shown, setShown] = useState(`${state}/${page}/${from}/${to}`);
  if (shown !== `${state}/${page}/${from}/${to}`) {
    setShown(`${state}/${page}/${from}/${to}`);
    setSelected([]);
    setRefusals(new Map());
  }

  const filters = { state, from, to };
  const { data, isPending, isError, error } = useQuery(expenseReimbursementsQueryOptions({ ...filters, page }));
  const forbidden = (error as ApiError | null)?.status === 403;
  const groups = data?.data ?? [];
  const everything = groups.flatMap((group) => group.entries);
  const picked = selected.filter((id) => everything.some((entry) => entry.id === id));

  const undo = useMutation({
    mutationFn: (entryIds: number[]) => undoExpensesReimbursed(entryIds),
    onSuccess: async (moved) => {
      setRefusals(new Map());
      setSelected([]);
      await queryClient.invalidateQueries({ queryKey: ["expenses"] });
      notifications.show({
        color: "teal",
        title: t("reimbursementUndone"),
        message: moved.length === 1 ? t("oneExpense") : t("countOfExpenses", { count: moved.length }),
      });
    },
    onError: (failure) => {
      const { byEntry, rest } = refusalsByEntry(failure);
      setRefusals(byEntry);
      if (rest.length > 0 || byEntry.size === 0) {
        notifications.show({
          color: "red",
          title: t("couldNotUndoReimbursement"),
          message: rest[0] ?? failure.message,
        });
      }
    },
  });

  /**
   * Two buttons on purpose. An absent or empty `entryIds` falls back to the
   * filters and downloads everything, so "Export selected" never sends one —
   * it is disabled with nothing picked, and "Export all" sends the filters
   * explicitly instead.
   */
  const exportCsv = useMutation({
    mutationFn: (entryIds?: number[]) => downloadReimbursementsCsv(filters, entryIds),
    onSuccess: (file) => {
      setExportError([]);
      saveCsv(file);
      notifications.show({ color: "teal", title: t("exportReady"), message: file.fileName });
    },
    onError: (failure) => {
      // The row-cap refusal carries no `errors` object at all — only a title
      // and a detail asking for a narrower filter — so both shapes are shown.
      const messages = refusalsByEntry(failure, "entryIds");
      const all = [...messages.rest, ...[...messages.byEntry.values()].flat()];
      setExportError(all.length > 0 ? all : [refusalMessage(failure)]);
    },
  });

  const toggle = (ids: number[], on: boolean) =>
    setSelected((current) =>
      on ? [...current, ...ids.filter((id) => !current.includes(id))] : current.filter((id) => !ids.includes(id)),
    );

  return (
    <Stack gap="lg">
      <PageHeader title={t("reimbursements")} description={t("reimbursementsDescription")} />

      <Group align="end" wrap="wrap">
        <SegmentedControl
          aria-label={t("reimbursementsState")}
          value={state}
          onChange={(next) => navigate({ search: { ...search, state: next, page: 1 } })}
          data={[
            { value: "waiting", label: t("waitingToBePaid") },
            { value: "reimbursed", label: t("alreadyPaid") },
          ]}
        />
        <DateInput
          label={t("fromDate")}
          valueFormat={t("dateInputFormat")}
          clearable
          w={170}
          value={from ?? null}
          onChange={(value) => navigate({ search: { ...search, from: value ?? undefined, page: 1 } })}
        />
        <DateInput
          label={t("toDate")}
          valueFormat={t("dateInputFormat")}
          clearable
          w={170}
          value={to ?? null}
          onChange={(value) => navigate({ search: { ...search, to: value ?? undefined, page: 1 } })}
        />
      </Group>

      <Group wrap="wrap">
        {state === "waiting" && (
          <Button disabled={picked.length === 0} onClick={() => setMarking(picked)}>
            {t("markSelectedReimbursed", { count: picked.length })}
          </Button>
        )}
        {state === "reimbursed" && (
          <Button
            variant="default"
            disabled={picked.length === 0}
            loading={undo.isPending}
            onClick={() => undo.mutate(picked)}
          >
            {t("undoSelectedReimbursement", { count: picked.length })}
          </Button>
        )}
        <Button
          variant="default"
          leftSection={<IconDownload size={16} />}
          loading={exportCsv.isPending && exportCsv.variables === undefined}
          onClick={() => exportCsv.mutate(undefined)}
        >
          {state === "waiting" ? t("exportAllWaiting") : t("exportAllPaid")}
        </Button>
        <Button
          variant="default"
          leftSection={<IconDownload size={16} />}
          disabled={picked.length === 0}
          loading={exportCsv.isPending && exportCsv.variables !== undefined}
          onClick={() => exportCsv.mutate(picked)}
        >
          {t("exportSelected", { count: picked.length })}
        </Button>
        {picked.length > 0 && (
          <Button variant="subtle" onClick={() => setSelected([])}>
            {t("clearSelection")}
          </Button>
        )}
      </Group>

      {exportError.length > 0 && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotExport")}>
          <RefusalList messages={exportError} />
        </Alert>
      )}

      {isError && !forbidden && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadReimbursements")}>
          {error.message}
        </Alert>
      )}
      {isPending && <ContentSkeleton rows={3} rowHeight={72} />}

      {forbidden && (
        <Card withBorder padding="lg" radius="md">
          <EmptyState
            icon={IconLock}
            title={t("reimbursementsForbidden")}
            description={t("reimbursementsForbiddenDescription")}
          />
        </Card>
      )}

      {data && groups.length === 0 && (
        <Card withBorder padding="lg" radius="md">
          <EmptyState
            icon={IconCash}
            title={state === "waiting" ? t("nothingOwedToAnyone") : t("nothingPaidYet")}
            description={state === "waiting" ? t("nothingOwedToAnyoneDescription") : t("nothingPaidYetDescription")}
          />
        </Card>
      )}

      {groups.map((group) => (
        <PersonCard key={group.user.userId} group={group} selected={picked} refusals={refusals} onToggle={toggle} />
      ))}

      {data && data.pagination.totalPages > 1 && (
        <Group justify="center">
          <Pagination
            total={data.pagination.totalPages}
            value={page}
            onChange={(next) => navigate({ search: { ...search, page: next } })}
          />
        </Group>
      )}

      <MarkReimbursedModal
        entryIds={marking}
        onClose={() => setMarking(null)}
        onDone={() => {
          setMarking(null);
          setSelected([]);
        }}
      />

      <Text size="xs" c="dimmed">
        {t("csvNote")}
      </Text>
    </Stack>
  );
};

const PersonCard = ({
  group,
  selected,
  refusals,
  onToggle,
}: {
  group: ExpenseReimbursementGroup;
  selected: number[];
  refusals: Map<number, string[]>;
  onToggle: (ids: number[], on: boolean) => void;
}) => {
  const { t } = useI18n("expenses");
  const format = useExpenseFormat();
  const ids = group.entries.map((entry) => entry.id);
  const allPicked = ids.length > 0 && ids.every((id) => selected.includes(id));

  return (
    <Card withBorder padding="lg" radius="md" data-reimbursement-group={group.user.userId}>
      <Stack gap="sm">
        <Group justify="space-between" align="start" wrap="wrap">
          <Stack gap={2}>
            <Title order={5}>{group.user.displayName}</Title>
            <CurrencyTotals totals={group.totals} owedOnly />
          </Stack>
          <Checkbox
            label={t("selectEveryoneOf", { person: group.user.displayName })}
            checked={allPicked}
            onChange={(event) => onToggle(ids, event.currentTarget.checked)}
          />
        </Group>

        <Table.ScrollContainer minWidth={760}>
          <Table striped highlightOnHover aria-label={t("expensesOf", { person: group.user.displayName })}>
            <Table.Thead>
              <Table.Tr>
                <Table.Th />
                <Table.Th>{t("date")}</Table.Th>
                <Table.Th>{t("description")}</Table.Th>
                <Table.Th>{t("project")}</Table.Th>
                <Table.Th>{t("owedToEmployee")}</Table.Th>
                <Table.Th>{t("paidBack")}</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {group.entries.map((entry: Expense) => (
                <Table.Tr key={entry.id} data-expense={entry.id}>
                  <Table.Td>
                    <Checkbox
                      aria-label={t("selectExpense", { description: entry.description })}
                      checked={selected.includes(entry.id)}
                      onChange={(event) => onToggle([entry.id], event.currentTarget.checked)}
                    />
                  </Table.Td>
                  <Table.Td>{format.date(entry.entryDate)}</Table.Td>
                  <Table.Td>
                    <Stack gap={2}>
                      <Text size="sm">{entry.description}</Text>
                      <RefusalList messages={refusals.get(entry.id) ?? []} />
                    </Stack>
                  </Table.Td>
                  <Table.Td>
                    <Text size="sm">{entry.project ? entry.project.code : t("notAvailable")}</Text>
                  </Table.Td>
                  <Table.Td>{format.money(entry.owedToEmployee, entry.currency)}</Table.Td>
                  <Table.Td>
                    {entry.reimbursement ? (
                      <Stack gap={0}>
                        <Text size="sm">{format.date(entry.reimbursement.date)}</Text>
                        {entry.reimbursement.reference && (
                          <Text size="xs" c="dimmed">
                            {entry.reimbursement.reference}
                          </Text>
                        )}
                      </Stack>
                    ) : (
                      <Text size="xs" c="dimmed">
                        {t("notAvailable")}
                      </Text>
                    )}
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        </Table.ScrollContainer>
      </Stack>
    </Card>
  );
};
