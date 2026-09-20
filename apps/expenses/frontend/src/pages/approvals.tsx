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
  VisuallyHidden,
} from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconChecks, IconLock, IconPencilDollar, IconReceiptOff } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  approveUnits,
  type ExpenseApprovalGroup,
  expenseApprovalsQueryOptions,
  unapproveUnits,
} from "../api/approvals";
import { type ClaimListItem, type ClaimSummary, expenseClaimsQueryOptions } from "../api/claims";
import { type Expense, expensesQueryOptions, type FlowUnits } from "../api/entries";
import { expensesMetaQueryOptions } from "../api/meta";
import { type ApiError, EXPENSES_QUERY_KEY } from "../api/request";
import { ClaimSummaryLine } from "../components/claim-summary";
import { CurrencyTotals } from "../components/currency-totals";
import { ExpenseStatusBadge } from "../components/expense-status-badge";
import { RefusalList } from "../components/refusal-list";
import "../i18n";
import { refusalsByUnit } from "../lib/errors";
import { useExpenseFormat } from "../lib/format";
import type { ApprovalsSearch } from "../lib/search";
import { expenseKindLabelKey } from "../lib/status";
import { ClaimDrawer } from "./-claim-drawer";
import { EntryDrawer } from "./-entry-drawer";
import { RejectModal } from "./-reject-modal";

export type { ApprovalsSearch } from "../lib/search";

/**
 * A trip as a queue row. The waiting queue carries `ExpensesClaimSummary`,
 * which counts the receipts missing and the rates replaced on the trip; the
 * approved half comes from `GET /claims`, which does not — an approved trip
 * has been looked at already, and those two flags are what an approver needs
 * *before* deciding. `flagsOf` is the one place that tells them apart.
 */
type ClaimRow = ClaimSummary | ClaimListItem;

const flagsOf = (claim: ClaimRow): ClaimSummary | undefined =>
  "receiptsMissing" in claim ? (claim as ClaimSummary) : undefined;

/**
 * Approvals (design §7): the submitted **units** this caller may approve —
 * standalone expenses and whole travel claims, grouped per person and
 * longest-waiting first — and, on the other half of the switch, the approved
 * ones, where an approval is taken back.
 *
 * A trip is one row and one unit: its lines are never listed as loose
 * expenses, and opening the row reads the claim itself. One selection spans
 * both kinds, and a decision on it is **one** request carrying `entryIds` and
 * `claimIds` together, because the server moves the batch all or nothing.
 *
 * The page needs nothing from the host: the queue's scope is the server's
 * answer, and every control comes from a unit's own `capabilities`.
 */
export const ApprovalsPage = () => {
  const { t } = useI18n("expenses");
  const search = useSearch({ strict: false }) as ApprovalsSearch;
  const navigate = useNavigate() as (options: unknown) => void;
  const { state = "waiting", page = 1, claimPage = 1 } = search;

  const [selected, setSelected] = useState<number[]>([]);
  const [selectedClaims, setSelectedClaims] = useState<number[]>([]);
  const [refusals, setRefusals] = useState<Map<number, string[]>>(new Map());
  const [claimRefusals, setClaimRefusals] = useState<Map<number, string[]>>(new Map());
  const [rejecting, setRejecting] = useState<FlowUnits | null>(null);
  const [opened, setOpened] = useState<Expense | null>(null);
  const [openedClaim, setOpenedClaim] = useState<ClaimRow | null>(null);

  // A selection belongs to the page and the half it was picked on; turning
  // either starts a new one. Adjusted during render from the previous
  // render's value, the way React documents.
  const shownKey = `${state}/${page}/${claimPage}`;
  const [shown, setShown] = useState(shownKey);
  if (shown !== shownKey) {
    setShown(shownKey);
    setSelected([]);
    setSelectedClaims([]);
    setRefusals(new Map());
    setClaimRefusals(new Map());
  }

  const { data: meta } = useQuery(expensesMetaQueryOptions());
  const zone = meta?.timeZone ?? "UTC";

  const queue = useQuery({ ...expenseApprovalsQueryOptions(page), enabled: state === "waiting" });
  const approved = useQuery({
    ...expensesQueryOptions({ status: "approved", standalone: true, page }),
    enabled: state === "approved",
  });
  // The approved trips come from their own paged endpoint, so they page on
  // their own key rather than being shuffled into the expenses' page — the
  // same honest split "My expenses" makes.
  const approvedClaims = useQuery({
    ...expenseClaimsQueryOptions({ status: "approved", page: claimPage }),
    enabled: state === "approved",
  });
  const active = state === "waiting" ? queue : approved;
  const forbidden = (active.error as ApiError | null)?.status === 403;

  const groups: ExpenseApprovalGroup[] = state === "waiting" ? (queue.data?.data ?? []) : [];
  const approvedEntries: Expense[] = state === "approved" ? (approved.data?.data ?? []) : [];
  const approvedTrips = state === "approved" ? (approvedClaims.data?.data ?? []) : [];

  const queuedEntries = state === "waiting" ? groups.flatMap((group) => group.entries) : approvedEntries;
  const queuedClaims: ClaimRow[] = state === "waiting" ? groups.flatMap((group) => group.claims) : approvedTrips;

  // Only a unit the caller may move offers a checkbox, and only such a unit
  // can be in the batch — the server is all or nothing across both lists, so
  // one unreachable id would refuse the lot.
  const movable = (capabilities: { canApprove: boolean; canUnapprove: boolean }) =>
    state === "waiting" ? capabilities.canApprove : capabilities.canUnapprove;
  const pickable = new Set(queuedEntries.filter((entry) => movable(entry.capabilities)).map((entry) => entry.id));
  const pickableClaims = new Set(queuedClaims.filter((claim) => movable(claim.capabilities)).map((claim) => claim.id));
  const picked = selected.filter((id) => pickable.has(id));
  const pickedClaims = selectedClaims.filter((id) => pickableClaims.has(id));
  const pickedUnits: FlowUnits = { entryIds: picked, claimIds: pickedClaims };
  const pickedCount = picked.length + pickedClaims.length;

  const queryClient = useQueryClient();
  const approve = useMutation({
    mutationFn: (units: FlowUnits) => approveUnits(units),
    onSuccess: async (moved) => {
      setRefusals(new Map());
      setClaimRefusals(new Map());
      setSelected([]);
      setSelectedClaims([]);
      await queryClient.invalidateQueries({ queryKey: [EXPENSES_QUERY_KEY] });
      const count = moved.entries.length + moved.claims.length;
      notifications.show({
        color: "teal",
        title: t("expensesApproved"),
        message: count === 1 ? t("oneExpense") : t("countOfExpenses", { count }),
      });
    },
    onError: (error) => {
      // Both lists are read and each sentence goes against the unit it names.
      // The two units number independently, so an expense 7 and a trip 7 are
      // different rows and are kept apart.
      const { byEntry, byClaim, rest } = refusalsByUnit(error);
      setRefusals(byEntry);
      setClaimRefusals(byClaim);
      if (rest.length > 0 || (byEntry.size === 0 && byClaim.size === 0)) {
        notifications.show({ color: "red", title: t("couldNotApprove"), message: rest[0] ?? error.message });
      }
    },
  });

  /**
   * The approved half's own batch. Taking an approval back is the same shape
   * as giving one — both lists in one request, all or nothing — so the
   * refusals land on their own rows the same way. A trip's `canUnapprove` can
   * be true while the server still refuses because one of its lines has been
   * invoiced, and that sentence is what the row then carries.
   */
  const unapproveAll = useMutation({
    mutationFn: (units: FlowUnits) => unapproveUnits(units),
    onSuccess: async (moved) => {
      setRefusals(new Map());
      setClaimRefusals(new Map());
      setSelected([]);
      setSelectedClaims([]);
      await queryClient.invalidateQueries({ queryKey: [EXPENSES_QUERY_KEY] });
      const count = moved.entries.length + moved.claims.length;
      notifications.show({
        color: "teal",
        title: t("expensesUnapproved"),
        message: count === 1 ? t("oneExpense") : t("countOfExpenses", { count }),
      });
    },
    onError: (error) => {
      const { byEntry, byClaim, rest } = refusalsByUnit(error);
      setRefusals(byEntry);
      setClaimRefusals(byClaim);
      if (rest.length > 0 || (byEntry.size === 0 && byClaim.size === 0)) {
        notifications.show({ color: "red", title: t("couldNotUnapprove"), message: rest[0] ?? error.message });
      }
    },
  });

  const toggle = (id: number, on: boolean) =>
    setSelected((current) => (on ? [...current, id] : current.filter((one) => one !== id)));
  const toggleClaim = (id: number, on: boolean) =>
    setSelectedClaims((current) => (on ? [...current, id] : current.filter((one) => one !== id)));

  const pagination = state === "waiting" ? queue.data?.pagination : approved.data?.pagination;
  const claimPagination = state === "approved" ? approvedClaims.data?.pagination : undefined;

  return (
    <Stack gap="lg">
      <PageHeader title={t("approvals")} description={t("approvalsDescription")} />

      <SegmentedControl
        aria-label={t("approvalsState")}
        value={state}
        onChange={(next) => navigate({ search: { state: next, page: 1, claimPage: 1 } })}
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

      {pickedCount > 0 && (
        <Group>
          {state === "waiting" ? (
            <>
              <Button loading={approve.isPending} onClick={() => approve.mutate(pickedUnits)}>
                {t("approveSelected", { count: pickedCount })}
              </Button>
              <Button color="red" variant="light" onClick={() => setRejecting(pickedUnits)}>
                {t("rejectSelected", { count: pickedCount })}
              </Button>
            </>
          ) : (
            <Button variant="default" loading={unapproveAll.isPending} onClick={() => unapproveAll.mutate(pickedUnits)}>
              {t("unapproveSelected", { count: pickedCount })}
            </Button>
          )}
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
              timeZone={zone}
              selected={picked}
              selectedClaims={pickedClaims}
              refusals={refusals}
              claimRefusals={claimRefusals}
              onToggle={toggle}
              onToggleClaim={toggleClaim}
              onOpen={setOpened}
              onOpenClaim={setOpenedClaim}
            />
          ))
        ))}

      {state === "approved" && approved.data && (
        <Card withBorder padding="lg" radius="md">
          {approvedEntries.length === 0 ? (
            <EmptyState title={t("nothingApproved")} description={t("nothingApprovedDescription")} />
          ) : (
            <EntryTable
              label={t("statusApproved")}
              entries={approvedEntries}
              refusals={refusals}
              selected={picked}
              selectable={(entry) => entry.capabilities.canUnapprove}
              onToggle={toggle}
              onOpen={setOpened}
            />
          )}
        </Card>
      )}

      {state === "approved" && approvedClaims.data && approvedTrips.length > 0 && (
        <Card withBorder padding="lg" radius="md">
          <Stack gap="sm">
            <Stack gap={0}>
              <Title order={5}>{t("travelClaims")}</Title>
              <Text size="sm" c="dimmed">
                {t("unitsPagedSeparately")}
              </Text>
            </Stack>
            <ClaimTable
              label={t("travelClaims")}
              claims={approvedTrips}
              timeZone={zone}
              refusals={claimRefusals}
              selected={pickedClaims}
              selectable={(claim) => claim.capabilities.canUnapprove}
              onToggle={toggleClaim}
              onOpen={setOpenedClaim}
            />
            {claimPagination && claimPagination.totalPages > 1 && (
              <Group justify="center">
                <Pagination
                  total={claimPagination.totalPages}
                  value={claimPage}
                  onChange={(next) => navigate({ search: { ...search, claimPage: next } })}
                />
              </Group>
            )}
          </Stack>
        </Card>
      )}

      {pagination && pagination.totalPages > 1 && (
        <Group justify="center">
          <Pagination
            total={pagination.totalPages}
            value={page}
            onChange={(next) => navigate({ search: { ...search, page: next } })}
          />
        </Group>
      )}

      <RejectModal
        units={rejecting}
        onClose={() => setRejecting(null)}
        onRejected={() => {
          setRejecting(null);
          setSelected([]);
          setSelectedClaims([]);
        }}
      />
      <EntryDrawer expense={opened} onClose={() => setOpened(null)} />
      <ClaimDrawer claim={openedClaim} onClose={() => setOpenedClaim(null)} />
    </Stack>
  );
};

const GroupCard = ({
  group,
  timeZone,
  selected,
  selectedClaims,
  refusals,
  claimRefusals,
  onToggle,
  onToggleClaim,
  onOpen,
  onOpenClaim,
}: {
  group: ExpenseApprovalGroup;
  timeZone: string;
  selected: number[];
  selectedClaims: number[];
  refusals: Map<number, string[]>;
  claimRefusals: Map<number, string[]>;
  onToggle: (id: number, on: boolean) => void;
  onToggleClaim: (id: number, on: boolean) => void;
  onOpen: (expense: Expense) => void;
  onOpenClaim: (claim: ClaimRow) => void;
}) => {
  const { t } = useI18n("expenses");
  return (
    <Card withBorder padding="lg" radius="md" data-approval-group={group.user.userId}>
      <Stack gap="sm">
        <Group justify="space-between" align="start" wrap="wrap">
          <Stack gap={2}>
            <Title order={5}>{group.user.displayName}</Title>
            {/* The group's figures already hold both kinds of unit, so nothing
                is added up here. */}
            <CurrencyTotals totals={group.totals} />
          </Stack>
          <Group gap="xs">
            {group.receiptsMissing > 0 && (
              <Badge color="orange" leftSection={<IconReceiptOff size={12} />}>
                {t("receiptsMissing", { count: group.receiptsMissing })}
              </Badge>
            )}
            {group.overriddenRates > 0 && (
              <Badge color="grape" leftSection={<IconPencilDollar size={12} />}>
                {t("overriddenRates", { count: group.overriddenRates })}
              </Badge>
            )}
          </Group>
        </Group>

        {group.claims.length > 0 && (
          <ClaimTable
            label={t("travelClaimsOf", { person: group.user.displayName })}
            claims={group.claims}
            timeZone={timeZone}
            refusals={claimRefusals}
            selected={selectedClaims}
            selectable={(claim) => claim.capabilities.canApprove}
            onToggle={onToggleClaim}
            onOpen={onOpenClaim}
          />
        )}

        {group.entries.length > 0 && (
          <EntryTable
            label={t("expensesOf", { person: group.user.displayName })}
            entries={group.entries}
            refusals={refusals}
            selected={selected}
            selectable={(entry) => entry.capabilities.canApprove}
            onToggle={onToggle}
            onOpen={onOpen}
          />
        )}
      </Stack>
    </Card>
  );
};

/**
 * The person's trips, one row each. A trip is a unit, not a run of lines: the
 * row says what it was for, where it went, when — in the installation's own
 * time zone — how many expenses it holds and what it comes to per currency,
 * and its lines are one click away in the drawer.
 *
 * A flag is a word **and** an icon, never a colour alone.
 */
const ClaimTable = ({
  label,
  claims,
  timeZone,
  refusals,
  selected,
  selectable,
  onToggle,
  onOpen,
}: {
  label: string;
  claims: ClaimRow[];
  timeZone: string;
  refusals: Map<number, string[]>;
  selected: number[];
  selectable: (claim: ClaimRow) => boolean;
  onToggle: (id: number, on: boolean) => void;
  onOpen: (claim: ClaimRow) => void;
}) => {
  const { t } = useI18n("expenses");
  return (
    <Table.ScrollContainer minWidth={760}>
      <Table striped highlightOnHover aria-label={label}>
        <Table.Thead>
          <Table.Tr>
            <Table.Th>
              <VisuallyHidden>{t("select")}</VisuallyHidden>
            </Table.Th>
            <Table.Th>{t("claimTrip")}</Table.Th>
            <Table.Th>{t("claimFlags")}</Table.Th>
            <Table.Th>{t("rowActions")}</Table.Th>
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {claims.map((claim) => (
            <Table.Tr key={claim.id} data-claim={claim.id}>
              <Table.Td>
                {selectable(claim) && (
                  <Checkbox
                    aria-label={t("selectTravelClaim", { purpose: claim.purpose })}
                    checked={selected.includes(claim.id)}
                    onChange={(event) => onToggle(claim.id, event.currentTarget.checked)}
                  />
                )}
              </Table.Td>
              <Table.Td>
                <Stack gap={2}>
                  <ClaimSummaryLine claim={claim} timeZone={timeZone} />
                  <RefusalList messages={refusals.get(claim.id) ?? []} />
                </Stack>
              </Table.Td>
              <Table.Td>
                <Group gap={4} wrap="wrap">
                  {flagsOf(claim)?.receiptsMissing ? (
                    <Badge color="orange" leftSection={<IconReceiptOff size={12} />}>
                      {t("receiptsMissing", { count: flagsOf(claim)?.receiptsMissing })}
                    </Badge>
                  ) : null}
                  {flagsOf(claim)?.overriddenRates ? (
                    <Badge color="grape" leftSection={<IconPencilDollar size={12} />}>
                      {t("overriddenRates", { count: flagsOf(claim)?.overriddenRates })}
                    </Badge>
                  ) : null}
                </Group>
              </Table.Td>
              <Table.Td>
                <Button
                  size="sm"
                  h={40}
                  variant="subtle"
                  aria-label={t("openTravelClaim", { purpose: claim.purpose })}
                  onClick={() => onOpen(claim)}
                >
                  {t("open")}
                </Button>
              </Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
    </Table.ScrollContainer>
  );
};

const EntryTable = ({
  label,
  entries,
  refusals,
  selected,
  selectable,
  onToggle,
  onOpen,
}: {
  label: string;
  entries: Expense[];
  refusals: Map<number, string[]>;
  selected: number[];
  selectable: (entry: Expense) => boolean;
  onToggle: (id: number, on: boolean) => void;
  onOpen: (expense: Expense) => void;
}) => {
  const { t } = useI18n("expenses");
  const format = useExpenseFormat();
  return (
    <Table.ScrollContainer minWidth={820}>
      <Table striped highlightOnHover aria-label={label}>
        <Table.Thead>
          <Table.Tr>
            <Table.Th>
              <VisuallyHidden>{t("select")}</VisuallyHidden>
            </Table.Th>
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
              <Table.Td>
                {selectable(entry) && (
                  <Checkbox
                    aria-label={t("selectExpense", { description: entry.description })}
                    checked={selected.includes(entry.id)}
                    onChange={(event) => onToggle(entry.id, event.currentTarget.checked)}
                  />
                )}
              </Table.Td>
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
                    <Badge size="xs" color="grape" leftSection={<IconPencilDollar size={10} />}>
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
