import { Alert, Badge, Button, Card, Group, Pagination, SegmentedControl, Stack, Text, Title } from "@mantine/core";
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
import { expenseClaimsQueryOptions } from "../api/claims";
import { type Expense, expensesQueryOptions, type FlowUnits } from "../api/entries";
import { expensesMetaQueryOptions } from "../api/meta";
import { type ApiError, EXPENSES_QUERY_KEY } from "../api/request";
import { CurrencyTotals } from "../components/currency-totals";
import "../i18n";
import { refusalsByUnit } from "../lib/errors";
import type { ApprovalsSearch } from "../lib/search";
import { type ClaimRow, ClaimTable, EntryTable } from "./-approval-tables";
import { ClaimDrawer } from "./-claim-drawer";
import { EntryDrawer } from "./-entry-drawer";
import { RejectModal } from "./-reject-modal";

export type { ApprovalsSearch } from "../lib/search";

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

  // A selection belongs to the half it was picked on **and to its own
  // section's page**. The two sections page independently, so paging the
  // trips must not throw away three expenses somebody has just ticked;
  // turning the switch changes both keys and clears both. Adjusted during
  // render from the previous render's value, the way React documents.
  const entryKey = `${state}/${page}`;
  const [shownEntries, setShownEntries] = useState(entryKey);
  if (shownEntries !== entryKey) {
    setShownEntries(entryKey);
    setSelected([]);
    setRefusals(new Map());
  }
  // The waiting queue is paged **by person** and carries both kinds inside a
  // group, so there the trips' scope is the queue's own page; only the
  // approved half pages them separately.
  const claimKey = state === "waiting" ? `${state}/${page}` : `${state}/${claimPage}`;
  const [shownClaims, setShownClaims] = useState(claimKey);
  if (shownClaims !== claimKey) {
    setShownClaims(claimKey);
    setSelectedClaims([]);
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
              {pickedCount === 1 ? t("unapproveSelectedOne") : t("unapproveSelected", { count: pickedCount })}
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

      {/* A failed read of the trips would otherwise leave a correct-looking
          page missing half its units, so it is said out loud. */}
      {state === "approved" && approvedClaims.isError && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadClaims")}>
          {approvedClaims.error.message}
        </Alert>
      )}

      {/* Both halves are units. "Nothing approved yet" is only true when
          neither list holds anything, and each card draws only when its own
          list does. */}
      {state === "approved" &&
        approved.data &&
        approvedClaims.data &&
        approvedEntries.length === 0 &&
        approvedTrips.length === 0 && (
          <Card withBorder padding="lg" radius="md">
            <EmptyState title={t("nothingApproved")} description={t("nothingApprovedDescription")} />
          </Card>
        )}

      {state === "approved" && approvedEntries.length > 0 && (
        <Card withBorder padding="lg" radius="md">
          <Stack gap="sm">
            <EntryTable
              label={t("statusApproved")}
              entries={approvedEntries}
              refusals={refusals}
              selected={picked}
              selectable={(entry) => entry.capabilities.canUnapprove}
              onToggle={toggle}
              onOpen={setOpened}
            />
            {/* Inside its own card: under the travel-claims card it looked
                like the pager of the trips, which have one of their own. */}
            {pagination && pagination.totalPages > 1 && (
              <Group justify="center">
                <Pagination
                  total={pagination.totalPages}
                  value={page}
                  onChange={(next) => navigate({ search: { ...search, page: next } })}
                />
              </Group>
            )}
          </Stack>
        </Card>
      )}

      {state === "approved" && approvedTrips.length > 0 && (
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

      {state === "waiting" && pagination && pagination.totalPages > 1 && (
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
