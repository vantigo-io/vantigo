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
  VisuallyHidden,
} from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconCash, IconDownload, IconLock } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import type { ClaimSummary } from "../api/claims";
import type { Expense, FlowUnits } from "../api/entries";
import { expensesMetaQueryOptions } from "../api/meta";
import {
  downloadReimbursementsCsv,
  type ExpenseReimbursementGroup,
  expenseReimbursementsQueryOptions,
  saveCsv,
  undoUnitsReimbursed,
} from "../api/reimbursements";
import { type ApiError, EXPENSES_QUERY_KEY } from "../api/request";
import { ClaimSummaryLine } from "../components/claim-summary";
import { CurrencyTotals } from "../components/currency-totals";
import { RefusalList } from "../components/refusal-list";
import "../i18n";
import { refusalMessage, refusalsByUnit } from "../lib/errors";
import { useExpenseFormat } from "../lib/format";
import type { ReimbursementsSearch } from "../lib/search";
import { MarkReimbursedModal } from "./-mark-reimbursed-modal";

export type { ReimbursementsSearch } from "../lib/search";

/**
 * Reimbursements (design §7): what a payroll run is made from, grouped per
 * person with their totals per currency, and — on the other half of the
 * switch — what has already been paid, where an undo is reachable.
 *
 * A **unit** is a standalone expense or a whole travel claim, and a trip is
 * paid as one: one row, for the sum of what its lines owe its owner. A
 * selection spans both kinds and a payroll run carries them in one request.
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
  const [selectedClaims, setSelectedClaims] = useState<number[]>([]);
  const [refusals, setRefusals] = useState<Map<number, string[]>>(new Map());
  const [claimRefusals, setClaimRefusals] = useState<Map<number, string[]>>(new Map());
  const [exportError, setExportError] = useState<string[]>([]);
  const [marking, setMarking] = useState<FlowUnits | null>(null);

  const [shown, setShown] = useState(`${state}/${page}/${from}/${to}`);
  if (shown !== `${state}/${page}/${from}/${to}`) {
    setShown(`${state}/${page}/${from}/${to}`);
    setSelected([]);
    setSelectedClaims([]);
    setRefusals(new Map());
    setClaimRefusals(new Map());
  }

  const filters = { state, from, to };
  const { data: meta } = useQuery(expensesMetaQueryOptions());
  const { data, isPending, isError, error } = useQuery(expenseReimbursementsQueryOptions({ ...filters, page }));
  const forbidden = (error as ApiError | null)?.status === 403;
  const groups = data?.data ?? [];
  const everything = groups.flatMap((group) => group.entries);
  const everyClaim = groups.flatMap((group) => group.claims);
  const picked = selected.filter((id) => everything.some((entry) => entry.id === id));
  const pickedClaims = selectedClaims.filter((id) => everyClaim.some((claim) => claim.id === id));
  const pickedUnits: FlowUnits = { entryIds: picked, claimIds: pickedClaims };
  const pickedCount = picked.length + pickedClaims.length;

  const undo = useMutation({
    mutationFn: (units: FlowUnits) => undoUnitsReimbursed(units),
    onSuccess: async (moved) => {
      setRefusals(new Map());
      setClaimRefusals(new Map());
      setSelected([]);
      setSelectedClaims([]);
      await queryClient.invalidateQueries({ queryKey: [EXPENSES_QUERY_KEY] });
      const count = moved.entries.length + moved.claims.length;
      notifications.show({
        color: "teal",
        title: t("reimbursementUndone"),
        message: count === 1 ? t("oneExpense") : t("countOfExpenses", { count }),
      });
    },
    onError: (failure) => {
      const { byEntry, byClaim, rest } = refusalsByUnit(failure);
      setRefusals(byEntry);
      setClaimRefusals(byClaim);
      if (rest.length > 0 || (byEntry.size === 0 && byClaim.size === 0)) {
        notifications.show({
          color: "red",
          title: t("couldNotUndoReimbursement"),
          message: rest[0] ?? failure.message,
        });
      }
    },
  });

  /**
   * Two buttons on purpose. A selection that is *present but names nothing*
   * is refused rather than read as "everything", so "Export everything" sends
   * no selection at all and the filters instead, while "Export selected"
   * sends only the list or lists that actually hold something — a selection
   * of trips alone carries no `entryIds` parameter.
   */
  const exportCsv = useMutation({
    mutationFn: (units?: FlowUnits) => downloadReimbursementsCsv(filters, units),
    onSuccess: (file) => {
      setExportError([]);
      saveCsv(file);
      notifications.show({ color: "teal", title: t("exportReady"), message: file.fileName });
    },
    onError: (failure) => {
      // The row-cap refusal carries no `errors` object at all — only a title
      // and a detail asking for a narrower filter — so both shapes are shown.
      const messages = refusalsByUnit(failure);
      const all = [
        ...messages.rest,
        ...[...messages.byEntry.values()].flat(),
        ...[...messages.byClaim.values()].flat(),
      ];
      setExportError(all.length > 0 ? all : [refusalMessage(failure)]);
    },
  });

  const toggle = (ids: number[], on: boolean) =>
    setSelected((current) =>
      on ? [...current, ...ids.filter((id) => !current.includes(id))] : current.filter((id) => !ids.includes(id)),
    );
  const toggleClaims = (ids: number[], on: boolean) =>
    setSelectedClaims((current) =>
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
          <Button disabled={pickedCount === 0} onClick={() => setMarking(pickedUnits)}>
            {t("markSelectedReimbursed", { count: pickedCount })}
          </Button>
        )}
        {state === "reimbursed" && (
          <Button
            variant="default"
            disabled={pickedCount === 0}
            loading={undo.isPending}
            onClick={() => undo.mutate(pickedUnits)}
          >
            {t("undoSelectedReimbursement", { count: pickedCount })}
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
          disabled={pickedCount === 0}
          loading={exportCsv.isPending && exportCsv.variables !== undefined}
          onClick={() => exportCsv.mutate(pickedUnits)}
        >
          {t("exportSelected", { count: pickedCount })}
        </Button>
        {pickedCount > 0 && (
          <Button
            variant="subtle"
            onClick={() => {
              setSelected([]);
              setSelectedClaims([]);
            }}
          >
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
        <PersonCard
          key={group.user.userId}
          group={group}
          timeZone={meta?.timeZone ?? "UTC"}
          selected={picked}
          selectedClaims={pickedClaims}
          refusals={refusals}
          claimRefusals={claimRefusals}
          onToggle={toggle}
          onToggleClaims={toggleClaims}
        />
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
        units={marking}
        onClose={() => setMarking(null)}
        onDone={() => {
          setMarking(null);
          setSelected([]);
          setSelectedClaims([]);
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
  timeZone,
  selected,
  selectedClaims,
  refusals,
  claimRefusals,
  onToggle,
  onToggleClaims,
}: {
  group: ExpenseReimbursementGroup;
  timeZone: string;
  selected: number[];
  selectedClaims: number[];
  refusals: Map<number, string[]>;
  claimRefusals: Map<number, string[]>;
  onToggle: (ids: number[], on: boolean) => void;
  onToggleClaims: (ids: number[], on: boolean) => void;
}) => {
  const { t } = useI18n("expenses");
  const format = useExpenseFormat();
  const ids = group.entries.map((entry) => entry.id);
  const claimIds = group.claims.map((claim) => claim.id);
  const allPicked =
    ids.length + claimIds.length > 0 &&
    ids.every((id) => selected.includes(id)) &&
    claimIds.every((id) => selectedClaims.includes(id));

  return (
    <Card withBorder padding="lg" radius="md" data-reimbursement-group={group.user.userId}>
      <Stack gap="sm">
        <Group justify="space-between" align="start" wrap="wrap">
          <Stack gap={2}>
            <Title order={5}>{group.user.displayName}</Title>
            {/* The group's totals already hold the claims' lines, so nothing
                is added up here. */}
            <CurrencyTotals totals={group.totals} owedOnly />
          </Stack>
          <Checkbox
            label={t("selectEveryoneOf", { person: group.user.displayName })}
            checked={allPicked}
            onChange={(event) => {
              onToggle(ids, event.currentTarget.checked);
              onToggleClaims(claimIds, event.currentTarget.checked);
            }}
          />
        </Group>

        {group.claims.length > 0 && (
          <Table.ScrollContainer minWidth={720}>
            <Table striped highlightOnHover aria-label={t("travelClaimsOf", { person: group.user.displayName })}>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>
                    <VisuallyHidden>{t("select")}</VisuallyHidden>
                  </Table.Th>
                  <Table.Th>{t("claimTrip")}</Table.Th>
                  <Table.Th>{t("owedToEmployee")}</Table.Th>
                  <Table.Th>{t("paidBack")}</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {group.claims.map((claim: ClaimSummary) => (
                  <Table.Tr key={claim.id} data-claim={claim.id}>
                    <Table.Td>
                      <Checkbox
                        aria-label={t("selectTravelClaim", { purpose: claim.purpose })}
                        checked={selectedClaims.includes(claim.id)}
                        onChange={(event) => onToggleClaims([claim.id], event.currentTarget.checked)}
                      />
                    </Table.Td>
                    <Table.Td>
                      <Stack gap={2}>
                        <ClaimSummaryLine claim={claim} timeZone={timeZone} />
                        <RefusalList messages={claimRefusals.get(claim.id) ?? []} />
                      </Stack>
                    </Table.Td>
                    <Table.Td>
                      <CurrencyTotals totals={claim.totals} owedOnly />
                    </Table.Td>
                    <Table.Td>
                      {/* The trip carries the payroll run's own stamp, the way
                          a loose expense does — the day in the installation's
                          zone, and the reference when the run had one. */}
                      {claim.reimbursement ? (
                        <Stack gap={0}>
                          <Text size="sm">{format.date(claim.reimbursement.date)}</Text>
                          {claim.reimbursement.reference && (
                            <Text size="xs" c="dimmed">
                              {claim.reimbursement.reference}
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
        )}

        {group.entries.length > 0 && (
          <Table.ScrollContainer minWidth={760}>
            <Table striped highlightOnHover aria-label={t("expensesOf", { person: group.user.displayName })}>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>
                    <VisuallyHidden>{t("select")}</VisuallyHidden>
                  </Table.Th>
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
        )}
      </Stack>
    </Card>
  );
};
