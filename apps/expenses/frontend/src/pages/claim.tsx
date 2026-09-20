import { ActionIcon, Alert, Badge, Button, Card, Group, Stack, Table, Text, Title } from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconPencil, IconPlus, IconSend, IconTrash } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { type Claim, deleteClaim, expenseClaimQueryOptions } from "../api/claims";
import { deleteExpense, type Expense, submitUnits } from "../api/entries";
import { expensesMetaQueryOptions } from "../api/meta";
import { type ApiError, EXPENSES_QUERY_KEY } from "../api/request";
import { ClaimDetails } from "../components/claim-details";
import { CurrencyTotals } from "../components/currency-totals";
import { ExpenseStatusBadge } from "../components/expense-status-badge";
import { ReceiptThumbnails } from "../components/receipt-thumbnails";
import { RefusalList } from "../components/refusal-list";
import "../i18n";
import { lineNamedIn, refusalMessage, refusalsByUnit } from "../lib/errors";
import { useExpenseFormat } from "../lib/format";
import { useLineName } from "../lib/line-name";
import { isKnownZone } from "../lib/time-zone";
import { ClaimFormModal, type ClaimModalState } from "./-claim-form-modal";
import { ExpenseFormModal, type ExpenseModalState } from "./-expense-form-modal";
import { CLAIM_LINE_CAP, PerDiemSection } from "./-per-diem-section";

export interface ClaimPageProps {
  /**
   * The travel claim being looked at. The host's route owns the path
   * parameter and hands it over as a number; no page in this package reads
   * the router's params or the session itself.
   */
  claimId: number;
}

/**
 * One travel claim: the trip, its per diem days, its driving, what was paid
 * for on it, and the one button that sends the whole thing for approval.
 *
 * Nothing here re-derives a rule. What may be done to the trip is what
 * `capabilities` said may be done to it; every amount is the server's; and a
 * trip's days are days in the installation's own time zone, which `/meta`
 * answers — never the browser's.
 */
export const ClaimPage = ({ claimId }: ClaimPageProps) => {
  const { t } = useI18n("expenses");
  const format = useExpenseFormat();
  const lineName = useLineName();
  const queryClient = useQueryClient();
  const navigate = useNavigate() as (options: unknown) => void;

  const [claimModal, setClaimModal] = useState<ClaimModalState | null>(null);
  const [lineModal, setLineModal] = useState<ExpenseModalState | null>(null);
  /** The sentences the submit refused with: the trip's own, and each line's. */
  const [refusals, setRefusals] = useState<string[]>([]);
  const [lineRefusals, setLineRefusals] = useState<Map<number, string[]>>(new Map());

  /**
   * A refusal describes the trip **as it was when it was refused**. Attaching
   * the receipt it asked for, or editing the line, makes it stale — and it
   * would otherwise stand until the next submit, telling the traveller to fix
   * something they have just fixed. Anything that can change the trip clears
   * it, and the next submit says what is still wrong.
   */
  const clearRefusals = () => {
    setRefusals([]);
    setLineRefusals(new Map());
  };

  const { data: meta, isPending: metaPending } = useQuery(expensesMetaQueryOptions());
  const { data: claim, isPending, isError, error } = useQuery(expenseClaimQueryOptions(claimId));

  const submit = useMutation({
    mutationFn: () => submitUnits({ claimIds: [claimId] }),
    onSuccess: async () => {
      clearRefusals();
      await queryClient.invalidateQueries({ queryKey: [EXPENSES_QUERY_KEY] });
      notifications.show({ color: "teal", title: t("claimSubmitted"), message: claim?.purpose ?? "" });
    },
    onError: (failure: Error) => {
      // A trip's refusals arrive on `claimIds`, and one of them may name the
      // line that stopped it — that sentence goes against the line as well as
      // at the top, where a refusal about the trip itself has to be read.
      const { byClaim, byEntry, rest } = refusalsByUnit(failure);
      const messages = [...(byClaim.get(claimId) ?? []), ...rest];
      setRefusals(messages.length > 0 ? messages : [failure.message]);
      const byLine = new Map(byEntry);
      for (const message of byClaim.get(claimId) ?? []) {
        const line = lineNamedIn(message);
        if (line !== undefined) byLine.set(line, [...(byLine.get(line) ?? []), message]);
      }
      setLineRefusals(byLine);
    },
  });

  const remove = useMutation({
    mutationFn: () => deleteClaim(claimId),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [EXPENSES_QUERY_KEY] });
      notifications.show({ color: "teal", title: t("claimDeleted"), message: "" });
      navigate({ to: "/expenses", search: { page: 1, claimPage: 1 } });
    },
    onError: (failure) =>
      notifications.show({ color: "red", title: t("couldNotDeleteClaim"), message: refusalMessage(failure) }),
  });

  const removeLine = useMutation({
    mutationFn: (id: number) => deleteExpense(id),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [EXPENSES_QUERY_KEY] });
      notifications.show({ color: "teal", title: t("expenseDeleted"), message: "" });
    },
    onError: (failure) =>
      notifications.show({ color: "red", title: t("couldNotDeleteExpense"), message: refusalMessage(failure) }),
  });

  // A trip's days are days in the installation's own zone, and every wall
  // clock on this page — the two instants it writes, and the two the edit form
  // captures the moment it opens — is derived from it. Rendering before
  // `/meta` has answered would label a trip in a guessed zone and, worse, let
  // the form capture one wall clock and send another.
  if (isPending || metaPending) return <ContentSkeleton rows={6} rowHeight={52} />;

  if (isError) {
    const status = (error as ApiError).status;
    if (status === 404 || status === 403) {
      return <EmptyState title={t("claimNotFound")} description={t("claimNotFoundDescription")} />;
    }
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadClaim")}>
        {error.message}
      </Alert>
    );
  }

  const zone = meta?.timeZone ?? "UTC";
  const editable = claim.capabilities.canEdit;
  // The modals unmount when the page turns read-only, but their state does
  // not: a trip submitted in another tab and later rejected would reopen a
  // stale edit form the moment the refetch landed.
  if (!editable && (claimModal || lineModal)) {
    setClaimModal(null);
    setLineModal(null);
  }
  const atCap = claim.lines.length >= CLAIM_LINE_CAP;
  const perDiemDays = claim.lines.filter((line) => line.kind === "per_diem");
  const mileageLines = claim.lines.filter((line) => line.kind === "mileage");
  const outlayLines = claim.lines.filter((line) => line.kind === "outlay");

  const header = (
    <PageHeader
      title={claim.purpose}
      description={claim.destination ?? t("travelClaimsDescription")}
      actions={
        <Group gap="xs" wrap="wrap">
          {editable && (
            <Button
              variant="default"
              leftSection={<IconPencil size={16} />}
              onClick={() => setClaimModal({ mode: "edit", claim })}
            >
              {t("editTravelClaim")}
            </Button>
          )}
          {claim.capabilities.canDelete && (
            <Button
              variant="default"
              color="red"
              leftSection={<IconTrash size={16} />}
              onClick={() =>
                modals.openConfirmModal({
                  title: t("deleteClaimTitle"),
                  children: <Text size="sm">{t("deleteClaimConfirm", { purpose: claim.purpose })}</Text>,
                  labels: { confirm: t("delete"), cancel: t("cancel") },
                  confirmProps: { color: "red" },
                  onConfirm: () => remove.mutate(),
                })
              }
            >
              {t("delete")}
            </Button>
          )}
          {claim.capabilities.canSubmit && (
            <Button leftSection={<IconSend size={16} />} loading={submit.isPending} onClick={() => submit.mutate()}>
              {t("submitClaim")}
            </Button>
          )}
        </Group>
      }
    />
  );

  const refusalBanner = refusals.length > 0 && (
    <Alert
      color="red"
      icon={<IconAlertCircle size={16} />}
      title={t("couldNotSubmitClaim")}
      data-testid="claim-refusals"
    >
      <RefusalList messages={refusals} />
    </Alert>
  );

  if (!editable) {
    return (
      <Stack gap="lg">
        {header}
        {refusalBanner}
        <Alert color="gray">{t("claimReadOnlyNotice")}</Alert>
        <Card withBorder padding="lg" radius="md">
          <ClaimDetails claim={claim} timeZone={zone} />
        </Card>
      </Stack>
    );
  }

  return (
    <Stack gap="lg">
      {header}
      {refusalBanner}

      <ClaimFormModal
        state={claimModal}
        onClose={() => {
          setClaimModal(null);
          clearRefusals();
        }}
      />
      <ExpenseFormModal
        state={lineModal}
        onClose={() => {
          setLineModal(null);
          clearRefusals();
        }}
      />

      <Card withBorder padding="lg" radius="md" data-testid="claim-trip">
        <Stack gap="xs">
          <Group gap="xs" wrap="wrap">
            <ExpenseStatusBadge status={claim.status} />
            <Badge variant="default">{claim.abroad ? t("claimAbroad") : t("claimDomestic")}</Badge>
            {claim.project && <Badge variant="default">{`${claim.project.code} · ${claim.project.name}`}</Badge>}
          </Group>
          <Text size="sm">
            {`${format.zonedDateTime(claim.departureAt, zone)} – ${format.zonedDateTime(claim.returnAt, zone)}`}
          </Text>
          <Text size="xs" c="dimmed">
            {/* A zone this browser cannot do arithmetic in is not the zone
                the times are being written in, so the label says so rather
                than naming one it is not using. */}
            {isKnownZone(zone) ? t("timesAreIn", { zone }) : t("timesAreInYourZone")}
          </Text>
          {claim.abroad && claim.abroadDayRate !== undefined && (
            <Text size="sm">
              {`${t("claimAbroadDayRate")}: ${format.money(claim.abroadDayRate, claim.abroadCurrency ?? "")}`}
            </Text>
          )}
          {claim.decision?.status === "rejected" && claim.decision.reason && (
            <Alert color="red" title={t("rejectedBecause", { reason: claim.decision.reason })}>
              {claim.decision.by
                ? t("rejectedBy", {
                    person: claim.decision.by.displayName,
                    date: format.zonedDate(claim.decision.at, zone),
                  })
                : t("rejectedOn", { date: format.zonedDate(claim.decision.at, zone) })}
            </Alert>
          )}
          {claim.reimbursement && (
            <Text size="sm" c="dimmed">
              {claim.reimbursement.reference
                ? `${t("reimbursedOn", { date: format.date(claim.reimbursement.date) })} · ${t("reimbursementReference", { reference: claim.reimbursement.reference })}`
                : t("reimbursedOn", { date: format.date(claim.reimbursement.date) })}
            </Text>
          )}
        </Stack>
      </Card>

      <Card withBorder padding="lg" radius="md" data-testid="claim-totals">
        <Stack gap={4}>
          <Title order={4}>{t("claimTotals")}</Title>
          <CurrencyTotals totals={claim.totals} />
          {claim.billableTotals && claim.billableTotals.length > 0 && (
            <Stack gap={0} data-testid="claim-billable-totals">
              {claim.billableTotals.map((total) => (
                <Text key={total.currency} size="sm">
                  {`${t("claimBillableTotal")}: ${format.money(total.amount, total.currency)}`}
                </Text>
              ))}
            </Stack>
          )}
        </Stack>
      </Card>

      <PerDiemSection claim={claim} days={perDiemDays} lineCount={claim.lines.length} refusals={lineRefusals} />

      <LineSection
        claim={claim}
        lines={mileageLines}
        kind="mileage"
        heading={t("mileageLines")}
        description={t("mileageLinesDescription")}
        addLabel={t("addMileageLine")}
        emptyLabel={t("noMileageLines")}
        atCap={atCap}
        refusals={lineRefusals}
        onAdd={() => setLineModal({ mode: "create", kind: "mileage", claim })}
        onEdit={(line) => setLineModal({ mode: "edit", expense: line, claim })}
        onRemove={(line) => {
          clearRefusals();
          removeLine.mutate(line.id);
        }}
        lineName={lineName}
      />

      <LineSection
        claim={claim}
        lines={outlayLines}
        kind="outlay"
        heading={t("outlayLines")}
        description={t("outlayLinesDescription")}
        addLabel={t("addOutlayLine")}
        emptyLabel={t("noOutlayLines")}
        atCap={atCap}
        refusals={lineRefusals}
        onAdd={() => setLineModal({ mode: "create", kind: "outlay", claim })}
        onEdit={(line) => setLineModal({ mode: "edit", expense: line, claim })}
        onRemove={(line) => {
          clearRefusals();
          removeLine.mutate(line.id);
        }}
        lineName={lineName}
      />
    </Stack>
  );
};

/**
 * One section of the trip's ordinary expenses. Mileage and outlays are the
 * same table with a different kind behind the button: the form is the
 * package's own expense form, opened with the trip so the line is recorded
 * against it.
 */
const LineSection = ({
  claim,
  lines,
  kind,
  heading,
  description,
  addLabel,
  emptyLabel,
  refusals,
  atCap,
  onAdd,
  onEdit,
  onRemove,
  lineName,
}: {
  claim: Claim;
  lines: Expense[];
  kind: "mileage" | "outlay";
  heading: string;
  description: string;
  addLabel: string;
  emptyLabel: string;
  refusals: Map<number, string[]>;
  /** Whether the trip already holds the 200 expenses a travel claim may hold. */
  atCap: boolean;
  onAdd: () => void;
  onEdit: (line: Expense) => void;
  onRemove: (line: Expense) => void;
  lineName: (line: Expense) => string;
}) => {
  const { t } = useI18n("expenses");
  const format = useExpenseFormat();
  const editable = claim.capabilities.canEdit;

  return (
    <Card withBorder padding="lg" radius="md" data-testid={`claim-${kind}-section`}>
      <Stack gap="md">
        <Group justify="space-between" align="start" wrap="wrap">
          <Stack gap={0}>
            <Title order={4}>{heading}</Title>
            <Text size="sm" c="dimmed">
              {description}
            </Text>
          </Stack>
          {editable && (
            <Button variant="light" leftSection={<IconPlus size={16} />} disabled={atCap} onClick={onAdd}>
              {addLabel}
            </Button>
          )}
        </Group>

        {editable && atCap && (
          <Text size="sm" c="orange">
            {t("claimLineCapReached")}
          </Text>
        )}

        {lines.length === 0 ? (
          <Text size="sm" c="dimmed">
            {emptyLabel}
          </Text>
        ) : (
          <Table.ScrollContainer minWidth={640}>
            <Table aria-label={heading}>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("date")}</Table.Th>
                  <Table.Th>{t("description")}</Table.Th>
                  <Table.Th>{t("amount")}</Table.Th>
                  {kind === "outlay" && <Table.Th>{t("receipts")}</Table.Th>}
                  <Table.Th>{t("rowActions")}</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {lines.map((line) => (
                  <Table.Tr key={line.id} data-expense={line.id}>
                    <Table.Td>{format.date(line.entryDate)}</Table.Td>
                    <Table.Td>
                      <Stack gap={2}>
                        <Text size="sm">{lineName(line)}</Text>
                        {line.kind === "mileage" && line.distanceKm !== undefined && (
                          <Text size="xs" c="dimmed">
                            {format.distance(line.distanceKm)}
                          </Text>
                        )}
                        <RefusalList messages={refusals.get(line.id) ?? []} />
                      </Stack>
                    </Table.Td>
                    <Table.Td>{format.money(line.grossAmount, line.currency)}</Table.Td>
                    {kind === "outlay" && (
                      <Table.Td>
                        {line.attachmentCount === 0 ? (
                          <Text size="xs" c="dimmed">
                            {t("noReceipts")}
                          </Text>
                        ) : (
                          <ReceiptThumbnails attachments={line.attachments} size={32} />
                        )}
                      </Table.Td>
                    )}
                    <Table.Td>
                      <Group gap={4} wrap="nowrap">
                        {line.capabilities.canEdit && (
                          <ActionIcon
                            variant="subtle"
                            aria-label={t("editExpense", { description: lineName(line) })}
                            onClick={() => onEdit(line)}
                          >
                            <IconPencil size={16} />
                          </ActionIcon>
                        )}
                        {line.capabilities.canDelete && (
                          <ActionIcon
                            variant="subtle"
                            color="red"
                            aria-label={t("deleteExpense", { description: lineName(line) })}
                            onClick={() =>
                              modals.openConfirmModal({
                                title: t("deleteExpenseTitle"),
                                children: (
                                  <Text size="sm">{t("deleteExpenseConfirm", { description: lineName(line) })}</Text>
                                ),
                                labels: { confirm: t("delete"), cancel: t("cancel") },
                                confirmProps: { color: "red" },
                                onConfirm: () => onRemove(line),
                              })
                            }
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
        )}
      </Stack>
    </Card>
  );
};
