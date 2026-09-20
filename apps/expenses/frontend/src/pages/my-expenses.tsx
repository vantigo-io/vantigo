import {
  ActionIcon,
  Alert,
  Button,
  Card,
  Checkbox,
  Group,
  Pagination,
  Select,
  Stack,
  Table,
  Text,
  VisuallyHidden,
} from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconPencil, IconPlus, IconSend, IconTrash } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { deleteExpense, type Expense, expensesQueryOptions, submitExpenses } from "../api/entries";
import { EXPENSES_QUERY_KEY } from "../api/request";
import { expenseStatsQueryOptions } from "../api/stats";
import { ExpenseStatusBadge } from "../components/expense-status-badge";
import { ReceiptThumbnails } from "../components/receipt-thumbnails";
import { RefusalList } from "../components/refusal-list";
import { StatusStrip } from "../components/status-strip";
import "../i18n";
import { refusalMessage, refusalsByEntry } from "../lib/errors";
import { useExpenseFormat } from "../lib/format";
import type { MyExpensesSearch } from "../lib/search";
import { expenseKindLabelKey, expenseKinds, expenseStatuses, expenseStatusLabelKey } from "../lib/status";
import { ExpenseFormModal, type ExpenseModalState } from "./-expense-form-modal";

export type { MyExpensesSearch } from "../lib/search";

export interface MyExpensesProps {
  /**
   * The caller's own user id. It comes from the host, which owns the session:
   * no page in this package reads the session or a permission itself. The
   * list is narrowed to it because "My expenses" is the caller's own — the
   * endpoint would otherwise widen for somebody who may see everybody's.
   */
  userId: string;
}

/**
 * My expenses (design §7): the three figures of the strip, the filters — all
 * of them in the URL — and the list, with the row actions each expense's own
 * `capabilities` allow. Nothing here re-derives a rule: what may be done to
 * an expense is what the server said may be done to it.
 */
export const MyExpensesPage = ({ userId }: MyExpensesProps) => {
  const { t } = useI18n("expenses");
  const format = useExpenseFormat();
  const queryClient = useQueryClient();
  const search = useSearch({ strict: false }) as MyExpensesSearch;
  const navigate = useNavigate() as (options: unknown) => void;
  const { page = 1, ...filters } = search;

  const [modalState, setModalState] = useState<ExpenseModalState | null>(null);
  const [selected, setSelected] = useState<number[]>([]);
  const [refusals, setRefusals] = useState<Map<number, string[]>>(new Map());

  // A selection belongs to the page *and the filters* it was picked on:
  // changing either starts a new one, so nothing stays checked behind a
  // filter that hides it. State adjusted during render from the previous
  // render's value, the way React documents, rather than an effect that would
  // flash the old bar.
  const shownKey = JSON.stringify(search);
  const [shown, setShown] = useState(shownKey);
  if (shown !== shownKey) {
    setShown(shownKey);
    setSelected([]);
    setRefusals(new Map());
  }

  const filterBy = (next: Partial<MyExpensesSearch>) => navigate({ search: { ...search, ...next, page: 1 } });

  const { data, isPending, isError, error } = useQuery(expensesQueryOptions({ ...filters, userId, page }));
  const { data: stats, isPending: statsPending } = useQuery(expenseStatsQueryOptions());

  const expenses = data?.data ?? [];
  const submittable = new Set(expenses.filter((one) => one.capabilities.canSubmit).map((one) => one.id));
  const picked = selected.filter((id) => submittable.has(id));

  const submit = useMutation({
    mutationFn: (entryIds: number[]) => submitExpenses(entryIds),
    onSuccess: async (moved) => {
      setRefusals(new Map());
      setSelected([]);
      await queryClient.invalidateQueries({ queryKey: [EXPENSES_QUERY_KEY] });
      notifications.show({
        color: "teal",
        title: t("expensesSubmitted"),
        message: moved.length === 1 ? t("oneExpense") : t("countOfExpenses", { count: moved.length }),
      });
    },
    onError: (error) => {
      const { byEntry, rest } = refusalsByEntry(error);
      setRefusals(byEntry);
      if (rest.length > 0 || byEntry.size === 0) {
        notifications.show({ color: "red", title: t("couldNotSubmit"), message: rest[0] ?? error.message });
      }
    },
  });

  const remove = useMutation({
    mutationFn: (id: number) => deleteExpense(id),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [EXPENSES_QUERY_KEY] });
      notifications.show({ color: "teal", title: t("expenseDeleted"), message: "" });
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("couldNotDeleteExpense"), message: refusalMessage(error) }),
  });

  const confirmDelete = (expense: Expense) =>
    modals.openConfirmModal({
      title: t("deleteExpenseTitle"),
      children: <Text size="sm">{t("deleteExpenseConfirm", { description: expense.description })}</Text>,
      labels: { confirm: t("delete"), cancel: t("cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => remove.mutate(expense.id),
    });

  const filtered = Object.values(filters).some((value) => value !== undefined);

  return (
    <Stack gap="lg">
      <PageHeader
        title={t("myExpenses")}
        description={t("myExpensesDescription")}
        actions={
          <Button leftSection={<IconPlus size={16} />} onClick={() => setModalState({ mode: "create" })}>
            {t("newExpense")}
          </Button>
        }
      />

      <StatusStrip stats={stats} loading={statsPending} />

      <ExpenseFormModal state={modalState} onClose={() => setModalState(null)} />

      <Card withBorder padding="lg" radius="md">
        <Stack gap="md">
          <Group align="end" wrap="wrap">
            <Select
              label={t("status")}
              placeholder={t("allStatuses")}
              clearable
              w={170}
              data={expenseStatuses.map((status) => ({ value: status, label: t(expenseStatusLabelKey(status)) }))}
              value={filters.status ?? null}
              onChange={(value) => filterBy({ status: (value ?? undefined) as MyExpensesSearch["status"] })}
            />
            <Select
              label={t("kind")}
              placeholder={t("allKinds")}
              clearable
              w={170}
              data={expenseKinds.map((kind) => ({ value: kind, label: t(expenseKindLabelKey(kind)) }))}
              value={filters.kind ?? null}
              onChange={(value) => filterBy({ kind: (value ?? undefined) as MyExpensesSearch["kind"] })}
            />
            <Select
              label={t("reimbursedFilter")}
              placeholder={t("allReimbursements")}
              clearable
              w={180}
              data={[
                { value: "true", label: t("reimbursedYes") },
                { value: "false", label: t("reimbursedNo") },
              ]}
              value={filters.reimbursed === undefined ? null : String(filters.reimbursed)}
              onChange={(value) => filterBy({ reimbursed: value === null ? undefined : value === "true" })}
            />
            <DateInput
              label={t("fromDate")}
              valueFormat={t("dateInputFormat")}
              clearable
              w={170}
              value={filters.from ?? null}
              onChange={(value) => filterBy({ from: value ?? undefined })}
            />
            <DateInput
              label={t("toDate")}
              valueFormat={t("dateInputFormat")}
              clearable
              w={170}
              value={filters.to ?? null}
              onChange={(value) => filterBy({ to: value ?? undefined })}
            />
          </Group>

          {picked.length > 0 && (
            <Group>
              <Button
                leftSection={<IconSend size={16} />}
                loading={submit.isPending}
                onClick={() => submit.mutate(picked)}
              >
                {t("submitSelected", { count: picked.length })}
              </Button>
              <Button variant="default" onClick={() => setSelected([])}>
                {t("clearSelection")}
              </Button>
            </Group>
          )}

          {isError && (
            <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadExpenses")}>
              {error.message}
            </Alert>
          )}

          {isPending && <ContentSkeleton rows={6} rowHeight={52} />}

          {data && (
            <>
              <Table.ScrollContainer minWidth={900}>
                <Table striped highlightOnHover aria-label={t("myExpenses")}>
                  <Table.Thead>
                    <Table.Tr>
                      <Table.Th>
                        <VisuallyHidden>{t("select")}</VisuallyHidden>
                      </Table.Th>
                      <Table.Th>{t("date")}</Table.Th>
                      <Table.Th>{t("description")}</Table.Th>
                      <Table.Th>{t("kind")}</Table.Th>
                      <Table.Th>{t("project")}</Table.Th>
                      <Table.Th>{t("amount")}</Table.Th>
                      <Table.Th>{t("status")}</Table.Th>
                      <Table.Th>{t("receipts")}</Table.Th>
                      <Table.Th>{t("rowActions")}</Table.Th>
                    </Table.Tr>
                  </Table.Thead>
                  <Table.Tbody>
                    {expenses.map((expense) => (
                      <Table.Tr key={expense.id} data-expense={expense.id}>
                        <Table.Td>
                          {expense.capabilities.canSubmit && (
                            <Checkbox
                              aria-label={t("selectExpense", { description: expense.description })}
                              checked={selected.includes(expense.id)}
                              onChange={(event) =>
                                setSelected((current) =>
                                  event.currentTarget.checked
                                    ? [...current, expense.id]
                                    : current.filter((id) => id !== expense.id),
                                )
                              }
                            />
                          )}
                        </Table.Td>
                        <Table.Td>{format.date(expense.entryDate)}</Table.Td>
                        <Table.Td>
                          <Stack gap={2}>
                            <Text size="sm">{expense.description}</Text>
                            {expense.decision?.status === "rejected" && expense.decision.reason && (
                              <>
                                <Text size="sm" c="red">
                                  {t("rejectedBecause", { reason: expense.decision.reason })}
                                </Text>
                                {expense.decision.by && (
                                  <Text size="xs" c="dimmed">
                                    {t("rejectedBy", {
                                      person: expense.decision.by.displayName,
                                      date: format.dateTime(expense.decision.at),
                                    })}
                                  </Text>
                                )}
                              </>
                            )}
                            {expense.reimbursement && (
                              <Text size="xs" c="dimmed">
                                {t("reimbursedOn", { date: format.date(expense.reimbursement.date) })}
                              </Text>
                            )}
                            <RefusalList messages={refusals.get(expense.id) ?? []} />
                          </Stack>
                        </Table.Td>
                        <Table.Td>
                          <Stack gap={0}>
                            <Text size="sm">{t(expenseKindLabelKey(expense.kind))}</Text>
                            {expense.kind === "mileage" && expense.distanceKm !== undefined && (
                              <Text size="xs" c="dimmed">
                                {format.distance(expense.distanceKm)}
                              </Text>
                            )}
                          </Stack>
                        </Table.Td>
                        <Table.Td>
                          <Text size="sm">{expense.project ? expense.project.code : t("notAvailable")}</Text>
                        </Table.Td>
                        <Table.Td>
                          <Stack gap={0}>
                            <Text size="sm">{format.money(expense.grossAmount, expense.currency)}</Text>
                            {expense.paidBy === "company" && (
                              <Text size="xs" c="dimmed">
                                {t("paidByCompany")}
                              </Text>
                            )}
                          </Stack>
                        </Table.Td>
                        <Table.Td>
                          <ExpenseStatusBadge status={expense.status} />
                        </Table.Td>
                        <Table.Td>
                          {expense.attachmentCount === 0 ? (
                            <Text size="xs" c="dimmed">
                              {t("noReceipts")}
                            </Text>
                          ) : (
                            <ReceiptThumbnails attachments={expense.attachments} size={32} />
                          )}
                        </Table.Td>
                        <Table.Td>
                          <Group gap={4} wrap="nowrap">
                            {expense.capabilities.canEdit && (
                              <ActionIcon
                                variant="subtle"
                                aria-label={t("editExpense", { description: expense.description })}
                                onClick={() => setModalState({ mode: "edit", expense })}
                              >
                                <IconPencil size={16} />
                              </ActionIcon>
                            )}
                            {expense.capabilities.canSubmit && (
                              <ActionIcon
                                variant="subtle"
                                aria-label={t("submitExpense", { description: expense.description })}
                                onClick={() => submit.mutate([expense.id])}
                              >
                                <IconSend size={16} />
                              </ActionIcon>
                            )}
                            {expense.capabilities.canDelete && (
                              <ActionIcon
                                variant="subtle"
                                color="red"
                                aria-label={t("deleteExpense", { description: expense.description })}
                                onClick={() => confirmDelete(expense)}
                              >
                                <IconTrash size={16} />
                              </ActionIcon>
                            )}
                          </Group>
                        </Table.Td>
                      </Table.Tr>
                    ))}
                  </Table.Tbody>
                </Table>
              </Table.ScrollContainer>

              {expenses.length === 0 && (
                <EmptyState
                  title={filtered ? t("noExpensesForFilter") : t("noExpenses")}
                  description={filtered ? t("noExpensesForFilterDescription") : t("noExpensesDescription")}
                  action={
                    filtered ? (
                      <Button variant="default" onClick={() => navigate({ search: { page: 1 } })}>
                        {t("clearFilters")}
                      </Button>
                    ) : undefined
                  }
                />
              )}

              {data.pagination.totalPages > 1 && (
                <Group justify="center">
                  <Pagination
                    total={data.pagination.totalPages}
                    value={page}
                    onChange={(next) => navigate({ search: { ...search, page: next } })}
                  />
                </Group>
              )}
            </>
          )}
        </Stack>
      </Card>
    </Stack>
  );
};
