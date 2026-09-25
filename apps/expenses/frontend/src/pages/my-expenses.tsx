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
  Title,
  VisuallyHidden,
} from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconPencil, IconPlus, IconRoute, IconSend, IconTrash } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { expenseClaimsQueryOptions } from "../api/claims";
import { deleteExpense, type Expense, expensesQueryOptions, submitUnits } from "../api/entries";
import { expensesMetaQueryOptions } from "../api/meta";
import { EXPENSES_QUERY_KEY } from "../api/request";
import { expenseStatsQueryOptions } from "../api/stats";
import { ClaimSummaryLine } from "../components/claim-summary";
import { ExpenseStatusBadge } from "../components/expense-status-badge";
import { ReceiptThumbnails } from "../components/receipt-thumbnails";
import { RefusalList } from "../components/refusal-list";
import { StatusStrip } from "../components/status-strip";
import "../i18n";
import { refusalMessage, refusalsByUnit } from "../lib/errors";
import { useExpenseFormat } from "../lib/format";
import { claimLinkOptions } from "../lib/routes";
import type { MyExpensesSearch } from "../lib/search";
import { expenseKindLabelKey, expenseStatuses, expenseStatusLabelKey, standaloneExpenseKinds } from "../lib/status";
import { ClaimFormModal, type ClaimModalState } from "./-claim-form-modal";
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
  const { page = 1, claimPage = 1, create, ...filters } = search;

  const [modalState, setModalState] = useState<ExpenseModalState | null>(null);
  // `?create=claim` opens the trip form on arrival — the host's Spotlight has
  // a "New travel claim" action and no button of this page to press, the same
  // seam every other app's create action uses. It is consumed once: closing
  // the form takes the parameter out of the URL, so a refresh does not reopen
  // it. State adjusted during render from the previous render's value.
  const [claimModal, setClaimModal] = useState<ClaimModalState | null>(create === "claim" ? { mode: "create" } : null);
  const [askedFor, setAskedFor] = useState(create);
  if (askedFor !== create) {
    setAskedFor(create);
    if (create === "claim") setClaimModal({ mode: "create" });
  }
  const [selected, setSelected] = useState<number[]>([]);
  const [selectedClaims, setSelectedClaims] = useState<number[]>([]);
  const [refusals, setRefusals] = useState<Map<number, string[]>>(new Map());
  const [claimRefusals, setClaimRefusals] = useState<Map<number, string[]>>(new Map());

  // A selection belongs to the page *and the filters* it was picked on:
  // changing either starts a new one, so nothing stays checked behind a
  // filter that hides it. State adjusted during render from the previous
  // render's value, the way React documents, rather than an effect that would
  // flash the old bar.
  // Each section's selection belongs to **its own** page and to the filters:
  // paging the trips must not throw away expenses somebody has just ticked,
  // which is the same rule the approval queue follows.
  const filterKey = JSON.stringify(filters);
  const entryKey = `${filterKey}/${page}`;
  const [shownEntries, setShownEntries] = useState(entryKey);
  if (shownEntries !== entryKey) {
    setShownEntries(entryKey);
    setSelected([]);
    setRefusals(new Map());
  }
  const claimKey = `${filterKey}/${claimPage}`;
  const [shownClaims, setShownClaims] = useState(claimKey);
  if (shownClaims !== claimKey) {
    setShownClaims(claimKey);
    setSelectedClaims([]);
    setClaimRefusals(new Map());
  }

  const filterBy = (next: Partial<MyExpensesSearch>) =>
    navigate({ search: { ...search, ...next, page: 1, claimPage: 1 } });

  const { data: meta } = useQuery(expensesMetaQueryOptions());
  const { data, isPending, isError, error } = useQuery(
    expensesQueryOptions({ ...filters, userId, standalone: true, page }),
  );
  // A trip is a unit of its own and comes from its own paged endpoint, so the
  // kind filter — which is about what *one expense* is — does not reach it.
  const {
    data: claimPageData,
    isError: claimsFailed,
    error: claimsError,
  } = useQuery(
    expenseClaimsQueryOptions({
      userId,
      status: filters.status,
      from: filters.from,
      to: filters.to,
      reimbursed: filters.reimbursed,
      page: claimPage,
    }),
  );
  const { data: stats, isPending: statsPending } = useQuery(expenseStatsQueryOptions());

  const expenses = data?.data ?? [];
  const claims = filters.kind ? [] : (claimPageData?.data ?? []);
  const submittable = new Set(expenses.filter((one) => one.capabilities.canSubmit).map((one) => one.id));
  const picked = selected.filter((id) => submittable.has(id));
  const submittableClaims = new Set(claims.filter((one) => one.capabilities.canSubmit).map((one) => one.id));
  const pickedClaims = selectedClaims.filter((id) => submittableClaims.has(id));

  const submit = useMutation({
    mutationFn: ({ entryIds, claimIds }: { entryIds: number[]; claimIds: number[] }) =>
      submitUnits({ entryIds, claimIds }),
    onSuccess: async (moved) => {
      setRefusals(new Map());
      setClaimRefusals(new Map());
      setSelected([]);
      setSelectedClaims([]);
      await queryClient.invalidateQueries({ queryKey: [EXPENSES_QUERY_KEY] });
      const count = moved.entries.length + moved.claims.length;
      notifications.show({
        color: "teal",
        title: t("expensesSubmitted"),
        message: count === 1 ? t("oneExpense") : t("countOfExpenses", { count }),
      });
    },
    onError: (error) => {
      // Both lists are read and each sentence goes against the unit it names.
      // A trip's refusal arrives on `claimIds`, which is a different list and
      // a different numbering from the expenses'.
      const { byEntry, byClaim, rest } = refusalsByUnit(error);
      setRefusals(byEntry);
      setClaimRefusals(byClaim);
      if (rest.length > 0 || (byEntry.size === 0 && byClaim.size === 0)) {
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
          <Group gap="xs" wrap="wrap">
            <Button
              variant="default"
              leftSection={<IconRoute size={16} />}
              onClick={() => setClaimModal({ mode: "create" })}
            >
              {t("newTravelClaim")}
            </Button>
            <Button leftSection={<IconPlus size={16} />} onClick={() => setModalState({ mode: "create" })}>
              {t("newExpense")}
            </Button>
          </Group>
        }
      />

      <StatusStrip stats={stats} loading={statsPending} />

      <ExpenseFormModal state={modalState} onClose={() => setModalState(null)} />
      <ClaimFormModal
        state={claimModal}
        onClose={() => {
          setClaimModal(null);
          // A **replace**: the intent is consumed, so Back must not put
          // `?create=claim` in the URL again and reopen the form somebody has
          // just closed. The same thing the customers list does with its own.
          if (create) navigate({ search: { ...search, create: undefined }, replace: true });
        }}
        onSaved={(saved) => navigate(claimLinkOptions(saved.id))}
      />

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
              // A supplier invoice lives on a project, so without the projects
              // module it is not a kind anybody can have recorded here.
              data={standaloneExpenseKinds
                .filter((kind) => kind !== "supplier_invoice" || meta?.projectsAvailable === true)
                .map((kind) => ({ value: kind, label: t(expenseKindLabelKey(kind)) }))}
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

          {picked.length + pickedClaims.length > 0 && (
            <Group>
              <Button
                leftSection={<IconSend size={16} />}
                loading={submit.isPending}
                onClick={() => submit.mutate({ entryIds: picked, claimIds: pickedClaims })}
              >
                {t("submitSelectedUnits", { count: picked.length + pickedClaims.length })}
              </Button>
              <Button
                variant="default"
                onClick={() => {
                  setSelected([]);
                  setSelectedClaims([]);
                }}
              >
                {t("clearSelection")}
              </Button>
            </Group>
          )}

          {isError && (
            <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadExpenses")}>
              {error.message}
            </Alert>
          )}
          {claimsFailed && (
            <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadClaims")}>
              {claimsError?.message}
            </Alert>
          )}
        </Stack>
      </Card>

      {/* Two kinds of unit, from two paged endpoints. They are listed one
          section under the other rather than shuffled into a single page: a
          merged page built from two pagers would either repeat rows or skip
          them, and a list that quietly loses an expense is worse than two
          honest ones. */}
      <Card withBorder padding="lg" radius="md" data-testid="my-travel-claims">
        <Stack gap="md">
          <Group justify="space-between" align="start" wrap="wrap">
            <Stack gap={0}>
              <Title order={4}>{t("travelClaims")}</Title>
              <Text size="sm" c="dimmed">
                {t("travelClaimsDescription")}
              </Text>
            </Stack>
          </Group>

          {filters.kind ? (
            <Text size="sm" c="dimmed">
              {t("claimsIgnoreKindFilter")}
            </Text>
          ) : claimPageData === undefined ? (
            // "You have none" is a fact about an answer, not about a request
            // in flight or one that failed — the alert above says what
            // happened, and this would contradict it.
            claimsFailed ? null : (
              <ContentSkeleton rows={2} rowHeight={52} />
            )
          ) : claims.length === 0 ? (
            <Text size="sm" c="dimmed">
              {filtered ? t("noTravelClaimsForFilter") : t("noTravelClaims")}
            </Text>
          ) : (
            <Table.ScrollContainer minWidth={760}>
              <Table striped highlightOnHover aria-label={t("travelClaims")}>
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>
                      <VisuallyHidden>{t("select")}</VisuallyHidden>
                    </Table.Th>
                    {/* The status is part of the trip's own line
                        (`ClaimSummaryLine`), so the row does not draw it twice. */}
                    <Table.Th>{t("claimTrip")}</Table.Th>
                    <Table.Th>{t("rowActions")}</Table.Th>
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {claims.map((claim) => (
                    <Table.Tr key={claim.id} data-claim={claim.id}>
                      <Table.Td>
                        {claim.capabilities.canSubmit && (
                          <Checkbox
                            aria-label={t("selectTravelClaim", { purpose: claim.purpose })}
                            checked={selectedClaims.includes(claim.id)}
                            onChange={(event) =>
                              setSelectedClaims((current) =>
                                event.currentTarget.checked
                                  ? [...current, claim.id]
                                  : current.filter((id) => id !== claim.id),
                              )
                            }
                          />
                        )}
                      </Table.Td>
                      <Table.Td>
                        <Stack gap={2}>
                          <ClaimSummaryLine claim={claim} timeZone={meta?.timeZone ?? "UTC"} />
                          <RefusalList messages={claimRefusals.get(claim.id) ?? []} />
                        </Stack>
                      </Table.Td>
                      <Table.Td>
                        <Group gap={4} wrap="nowrap">
                          <Button
                            size="sm"
                            h={40}
                            variant="subtle"
                            aria-label={t("openTravelClaim", { purpose: claim.purpose })}
                            onClick={() => navigate(claimLinkOptions(claim.id))}
                          >
                            {t("open")}
                          </Button>
                          {claim.capabilities.canSubmit && (
                            <ActionIcon
                              variant="subtle"
                              loading={submit.isPending}
                              aria-label={t("submitTravelClaim", { purpose: claim.purpose })}
                              onClick={() => submit.mutate({ entryIds: [], claimIds: [claim.id] })}
                            >
                              <IconSend size={16} />
                            </ActionIcon>
                          )}
                        </Group>
                      </Table.Td>
                    </Table.Tr>
                  ))}
                </Table.Tbody>
              </Table>
            </Table.ScrollContainer>
          )}

          {claimPageData && claimPageData.pagination.totalPages > 1 && !filters.kind && (
            <Group justify="center">
              <Pagination
                total={claimPageData.pagination.totalPages}
                value={claimPage}
                onChange={(next) => navigate({ search: { ...search, claimPage: next } })}
              />
            </Group>
          )}
        </Stack>
      </Card>

      <Card withBorder padding="lg" radius="md">
        <Stack gap="md">
          <Stack gap={0}>
            <Title order={4}>{t("expensesSection")}</Title>
            <Text size="sm" c="dimmed">
              {t("unitsPagedSeparately")}
            </Text>
          </Stack>

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
                                <Text size="xs" c="dimmed">
                                  {expense.decision.by
                                    ? t("rejectedBy", {
                                        person: expense.decision.by.displayName,
                                        date: format.dateTime(expense.decision.at),
                                      })
                                    : t("rejectedOn", { date: format.dateTime(expense.decision.at) })}
                                </Text>
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
                              {expense.kind === "supplier_invoice" ? t("noSupplierInvoiceDocument") : t("noReceipts")}
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
                                loading={submit.isPending}
                                aria-label={t("submitExpense", { description: expense.description })}
                                onClick={() => submit.mutate({ entryIds: [expense.id], claimIds: [] })}
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
