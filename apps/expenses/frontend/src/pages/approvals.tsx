import {
  Alert,
  Badge,
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
  UnstyledButton,
} from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconChecks, IconLock } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { approveExpenses, type ExpenseApprovalGroup, expenseApprovalsQueryOptions } from "../api/approvals";
import { type Expense, expensesQueryOptions } from "../api/entries";
import { type ApiError, EXPENSES_QUERY_KEY } from "../api/request";
import { CurrencyTotals } from "../components/currency-totals";
import { ExpenseStatusBadge } from "../components/expense-status-badge";
import { RefusalList } from "../components/refusal-list";
import "../i18n";
import { refusalsByEntry } from "../lib/errors";
import { useExpenseFormat } from "../lib/format";
import type { ApprovalsSearch } from "../lib/search";
import { expenseKindLabelKey } from "../lib/status";
import { EntryDrawer } from "./-entry-drawer";
import { RejectModal } from "./-reject-modal";

export type { ApprovalsSearch } from "../lib/search";

/**
 * Approvals (design §7): the submitted expenses this caller may approve,
 * grouped per person and longest-waiting first, and — on the other half of
 * the switch — the approved ones, where an approval is taken back.
 *
 * The page needs nothing from the host: the queue's scope is the server's
 * answer, and every control comes from an entry's own `capabilities`.
 */
export const ApprovalsPage = () => {
  const { t } = useI18n("expenses");
  const search = useSearch({ strict: false }) as ApprovalsSearch;
  const navigate = useNavigate() as (options: unknown) => void;
  const { state = "waiting", page = 1 } = search;

  const [selected, setSelected] = useState<number[]>([]);
  const [refusals, setRefusals] = useState<Map<number, string[]>>(new Map());
  const [rejecting, setRejecting] = useState<number[] | null>(null);
  const [opened, setOpened] = useState<Expense | null>(null);

  // A selection belongs to the page and the half it was picked on; turning
  // either starts a new one. Adjusted during render from the previous
  // render's value, the way React documents.
  const [shown, setShown] = useState(`${state}/${page}`);
  if (shown !== `${state}/${page}`) {
    setShown(`${state}/${page}`);
    setSelected([]);
    setRefusals(new Map());
  }

  const queue = useQuery({ ...expenseApprovalsQueryOptions(page), enabled: state === "waiting" });
  const approved = useQuery({
    ...expensesQueryOptions({ status: "approved", page }),
    enabled: state === "approved",
  });
  const active = state === "waiting" ? queue : approved;
  const forbidden = (active.error as ApiError | null)?.status === 403;

  const groups: ExpenseApprovalGroup[] = state === "waiting" ? (queue.data?.data ?? []) : [];
  const approvedEntries: Expense[] = state === "approved" ? (approved.data?.data ?? []) : [];
  const queued = groups.flatMap((group) => group.entries);
  // Only an entry the caller may approve offers a checkbox, and only such an
  // entry can be in the batch — the server is all or nothing, so one
  // unreachable id would refuse the lot.
  const approvable = new Set(queued.filter((entry) => entry.capabilities.canApprove).map((entry) => entry.id));
  const picked = selected.filter((id) => approvable.has(id));

  const queryClient = useQueryClient();
  const approve = useMutation({
    mutationFn: (entryIds: number[]) => approveExpenses(entryIds),
    onSuccess: async (moved) => {
      setRefusals(new Map());
      setSelected([]);
      await queryClient.invalidateQueries({ queryKey: [EXPENSES_QUERY_KEY] });
      notifications.show({
        color: "teal",
        title: t("expensesApproved"),
        message: moved.length === 1 ? t("oneExpense") : t("countOfExpenses", { count: moved.length }),
      });
    },
    onError: (error) => {
      const { byEntry, rest } = refusalsByEntry(error);
      setRefusals(byEntry);
      if (rest.length > 0 || byEntry.size === 0) {
        notifications.show({ color: "red", title: t("couldNotApprove"), message: rest[0] ?? error.message });
      }
    },
  });

  const toggle = (id: number, on: boolean) =>
    setSelected((current) => (on ? [...current, id] : current.filter((one) => one !== id)));

  const pagination = state === "waiting" ? queue.data?.pagination : approved.data?.pagination;

  return (
    <Stack gap="lg">
      <PageHeader title={t("approvals")} description={t("approvalsDescription")} />

      <SegmentedControl
        aria-label={t("approvalsState")}
        value={state}
        onChange={(next) => navigate({ search: { state: next, page: 1 } })}
        data={[
          { value: "waiting", label: t("waitingForApproval") },
          { value: "approved", label: t("statusApproved") },
        ]}
      />

      {active.isError && !forbidden && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadApprovals")}>
          {active.error.message}
        </Alert>
      )}
      {active.isPending && <ContentSkeleton rows={3} rowHeight={72} />}

      {forbidden && (
        <Card withBorder padding="lg" radius="md">
          <EmptyState icon={IconLock} title={t("approvesNothing")} description={t("approvesNothingDescription")} />
        </Card>
      )}

      {picked.length > 0 && (
        <Group>
          <Button loading={approve.isPending} onClick={() => approve.mutate(picked)}>
            {t("approveSelected", { count: picked.length })}
          </Button>
          <Button color="red" variant="light" onClick={() => setRejecting(picked)}>
            {t("rejectSelected", { count: picked.length })}
          </Button>
          <Button variant="default" onClick={() => setSelected([])}>
            {t("clearSelection")}
          </Button>
        </Group>
      )}

      {state === "waiting" &&
        queue.data &&
        (groups.length === 0 ? (
          <Card withBorder padding="lg" radius="md">
            <EmptyState
              icon={IconChecks}
              title={t("nothingToApprove")}
              description={t("nothingToApproveDescription")}
            />
          </Card>
        ) : (
          groups.map((group) => (
            <GroupCard
              key={group.user.userId}
              group={group}
              selected={picked}
              refusals={refusals}
              onToggle={toggle}
              onOpen={setOpened}
            />
          ))
        ))}

      {state === "approved" && approved.data && (
        <Card withBorder padding="lg" radius="md">
          {approvedEntries.length === 0 ? (
            <EmptyState title={t("nothingApproved")} description={t("nothingApprovedDescription")} />
          ) : (
            <EntryTable label={t("statusApproved")} entries={approvedEntries} refusals={refusals} onOpen={setOpened} />
          )}
        </Card>
      )}

      {pagination && pagination.totalPages > 1 && (
        <Group justify="center">
          <Pagination
            total={pagination.totalPages}
            value={page}
            onChange={(next) => navigate({ search: { state, page: next } })}
          />
        </Group>
      )}

      <RejectModal
        entryIds={rejecting}
        onClose={() => setRejecting(null)}
        onRejected={() => {
          setRejecting(null);
          setSelected([]);
        }}
      />
      <EntryDrawer expense={opened} onClose={() => setOpened(null)} />
    </Stack>
  );
};

const GroupCard = ({
  group,
  selected,
  refusals,
  onToggle,
  onOpen,
}: {
  group: ExpenseApprovalGroup;
  selected: number[];
  refusals: Map<number, string[]>;
  onToggle: (id: number, on: boolean) => void;
  onOpen: (expense: Expense) => void;
}) => {
  const { t } = useI18n("expenses");
  return (
    <Card withBorder padding="lg" radius="md" data-approval-group={group.user.userId}>
      <Stack gap="sm">
        <Group justify="space-between" align="start" wrap="wrap">
          <Stack gap={2}>
            <Title order={5}>{group.user.displayName}</Title>
            <CurrencyTotals totals={group.totals} />
          </Stack>
          <Group gap="xs">
            {group.receiptsMissing > 0 && (
              <Badge color="orange">{t("receiptsMissing", { count: group.receiptsMissing })}</Badge>
            )}
            {group.overriddenRates > 0 && (
              <Badge color="grape">{t("overriddenRates", { count: group.overriddenRates })}</Badge>
            )}
          </Group>
        </Group>
        <EntryTable
          label={t("expensesOf", { person: group.user.displayName })}
          entries={group.entries}
          refusals={refusals}
          selected={selected}
          onToggle={onToggle}
          onOpen={onOpen}
        />
      </Stack>
    </Card>
  );
};

const EntryTable = ({
  label,
  entries,
  refusals,
  selected,
  onToggle,
  onOpen,
}: {
  label: string;
  entries: Expense[];
  refusals: Map<number, string[]>;
  selected?: number[];
  onToggle?: (id: number, on: boolean) => void;
  onOpen: (expense: Expense) => void;
}) => {
  const { t } = useI18n("expenses");
  const format = useExpenseFormat();
  return (
    <Table.ScrollContainer minWidth={820}>
      <Table striped highlightOnHover aria-label={label}>
        <Table.Thead>
          <Table.Tr>
            {onToggle && <Table.Th />}
            <Table.Th>{t("date")}</Table.Th>
            <Table.Th>{t("description")}</Table.Th>
            <Table.Th>{t("kind")}</Table.Th>
            <Table.Th>{t("project")}</Table.Th>
            <Table.Th>{t("grossAmount")}</Table.Th>
            <Table.Th>{t("owedToEmployee")}</Table.Th>
            <Table.Th>{t("receipts")}</Table.Th>
            <Table.Th>{t("status")}</Table.Th>
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {entries.map((entry) => (
            <Table.Tr key={entry.id} data-expense={entry.id}>
              {onToggle && (
                <Table.Td>
                  {entry.capabilities.canApprove && (
                    <Checkbox
                      aria-label={t("selectExpense", { description: entry.description })}
                      checked={selected?.includes(entry.id) ?? false}
                      onChange={(event) => onToggle(entry.id, event.currentTarget.checked)}
                    />
                  )}
                </Table.Td>
              )}
              <Table.Td>{format.date(entry.entryDate)}</Table.Td>
              <Table.Td>
                <Stack gap={2}>
                  <UnstyledButton
                    aria-label={t("openExpense", { description: entry.description })}
                    onClick={() => onOpen(entry)}
                  >
                    <Text size="sm" td="underline">
                      {entry.description}
                    </Text>
                  </UnstyledButton>
                  <RefusalList messages={refusals.get(entry.id) ?? []} />
                </Stack>
              </Table.Td>
              <Table.Td>
                <Group gap={4} wrap="nowrap">
                  <Text size="sm">{t(expenseKindLabelKey(entry.kind))}</Text>
                  {entry.rateOverride && (
                    <Badge size="xs" color="grape">
                      {t("rateOverridden")}
                    </Badge>
                  )}
                </Group>
              </Table.Td>
              <Table.Td>
                <Text size="sm">{entry.project ? entry.project.code : t("notAvailable")}</Text>
              </Table.Td>
              <Table.Td>{format.money(entry.grossAmount, entry.currency)}</Table.Td>
              <Table.Td>{format.money(entry.owedToEmployee, entry.currency)}</Table.Td>
              <Table.Td>
                {entry.kind === "mileage" ? (
                  <Text size="xs" c="dimmed">
                    {t("notAvailable")}
                  </Text>
                ) : entry.attachmentCount === 0 ? (
                  <Text size="xs" c="orange">
                    {t("receiptMissing")}
                  </Text>
                ) : (
                  <Text size="xs">
                    {entry.attachmentCount === 1
                      ? t("oneReceipt")
                      : t("receiptCount", { count: entry.attachmentCount })}
                  </Text>
                )}
              </Table.Td>
              <Table.Td>
                <ExpenseStatusBadge status={entry.status} size="sm" />
              </Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
    </Table.ScrollContainer>
  );
};
