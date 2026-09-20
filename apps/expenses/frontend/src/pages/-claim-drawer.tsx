import { Alert, Button, Drawer, Group, Stack } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { approveUnits, unapproveUnits } from "../api/approvals";
import { type Claim, expenseClaimQueryOptions } from "../api/claims";
import type { Expense } from "../api/entries";
import { expensesMetaQueryOptions } from "../api/meta";
import { EXPENSES_QUERY_KEY } from "../api/request";
import { ClaimDetails } from "../components/claim-details";
import { RefusalList } from "../components/refusal-list";
import "../i18n";
import { lineNamedIn, refusalsByUnit } from "../lib/errors";
import { useLineName } from "../lib/line-name";
import { BillingModal } from "./-billing-modal";
import { MarkInvoicedModal } from "./-mark-invoiced-modal";
import { RateOverrideModal } from "./-rate-override-modal";
import { RejectModal } from "./-reject-modal";
import { useUndoInvoiced } from "./-undo-invoiced";

export interface ClaimDrawerProps {
  /**
   * The trip being looked at — whatever the row it was opened from carries,
   * which is only ever its id and what it was for — or null when the drawer
   * is closed. Everything else is read from the claim itself.
   */
  claim: { id: number; purpose: string } | null;
  onClose: () => void;
}

/**
 * One travel claim in an approver's hands: the whole trip read-only — its
 * header, its per diem days, its driving, what was paid for on it, the totals,
 * the decision and the reimbursement stamp — with the decisions the *claim's*
 * capabilities allow and the doors each *line's* own capabilities allow.
 *
 * The queue carries a trip at a glance and never its lines, so the drawer
 * reads the claim itself (`GET /claims/{id}`) the moment it opens. A decision
 * is the claim's: approve, reject and unapprove each send one `claimIds` of
 * one. Pricing, invoicing and a rate override stay the line's own doors,
 * because each of those is about one amount.
 */
export const ClaimDrawer = ({ claim, onClose }: ClaimDrawerProps) => {
  const { t } = useI18n("expenses");
  return (
    <Drawer
      opened={claim !== null}
      onClose={onClose}
      position="right"
      size="xl"
      title={claim?.purpose ?? t("travelClaim")}
    >
      {claim && <ClaimActions key={claim.id} claimId={claim.id} onClose={onClose} />}
    </Drawer>
  );
};

const ClaimActions = ({ claimId, onClose }: { claimId: number; onClose: () => void }) => {
  const { t } = useI18n("expenses");
  const queryClient = useQueryClient();
  const lineName = useLineName();

  const [refusals, setRefusals] = useState<string[]>([]);
  const [lineRefusals, setLineRefusals] = useState<Map<number, string[]>>(new Map());
  const [rejecting, setRejecting] = useState(false);
  const [overriding, setOverriding] = useState<Expense | null>(null);
  const [pricing, setPricing] = useState<Expense | null>(null);
  const [invoicing, setInvoicing] = useState<Expense | null>(null);
  /**
   * What a write in this drawer answered, per line. A guarded write carries
   * the revision **its own last answer gave**, so two writes on one line do
   * not race each other into a 409; a refetch that brings a higher revision
   * wins, because then the server has moved past what this drawer holds.
   */
  const [written, setWritten] = useState<Map<number, Expense>>(new Map());

  const { data: meta, isPending: metaPending } = useQuery(expensesMetaQueryOptions());
  const { data: claim, isPending, isError, error } = useQuery(expenseClaimQueryOptions(claimId));

  const saved = (line: Expense) => {
    setWritten((current) => new Map(current).set(line.id, line));
    setOverriding(null);
    setPricing(null);
    setInvoicing(null);
  };

  const undoInvoiced = useUndoInvoiced(saved);

  /** Approve and unapprove differ only in the call and the words; the handling is one shape. */
  const decided = (title: string, failure: string) => ({
    onSuccess: async () => {
      setRefusals([]);
      setLineRefusals(new Map());
      await queryClient.invalidateQueries({ queryKey: [EXPENSES_QUERY_KEY] });
      notifications.show({ color: "teal", title, message: claim?.purpose ?? "" });
      onClose();
    },
    onError: (failed: Error) => {
      // A trip's refusals arrive on `claimIds`, and one of them may name the
      // line that stopped it — `canUnapprove` can be true while the server
      // refuses because a line has been invoiced, and that sentence has to be
      // read rather than swallowed.
      const { byClaim, byEntry, rest } = refusalsByUnit(failed);
      const own = byClaim.get(claimId) ?? [];
      const messages = [...own, ...rest];
      setRefusals(messages.length > 0 ? messages : [failed.message]);
      const byLine = new Map(byEntry);
      for (const message of own) {
        const line = lineNamedIn(message);
        if (line !== undefined) byLine.set(line, [...(byLine.get(line) ?? []), message]);
      }
      setLineRefusals(byLine);
      notifications.show({ color: "red", title: failure, message: "" });
    },
  });

  const approve = useMutation({
    mutationFn: () => approveUnits({ claimIds: [claimId] }),
    ...decided(t("expensesApproved"), t("couldNotApprove")),
  });
  const unapprove = useMutation({
    mutationFn: () => unapproveUnits({ claimIds: [claimId] }),
    ...decided(t("expensesUnapproved"), t("couldNotUnapprove")),
  });

  // The trip's two instants are written in the installation's own zone, so
  // the drawer waits for it rather than labelling them in a guessed one.
  if (isPending || metaPending) return <ContentSkeleton rows={5} rowHeight={48} />;
  if (isError) {
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadClaim")}>
        {error.message}
      </Alert>
    );
  }

  /** The line as this drawer knows it: the trip's copy, or a later answer of its own. */
  const shown = (line: Expense): Expense => {
    const written_ = written.get(line.id);
    return written_ && written_.revision >= line.revision ? written_ : line;
  };

  const lineActions = (line: Expense) => {
    const current = shown(line);
    return (
      <Group gap={4} wrap="nowrap">
        {current.capabilities.canOverrideRate && (
          <Button
            size="xs"
            h={40}
            variant="default"
            aria-label={t("overrideRateFor", { description: lineName(current) })}
            onClick={() => setOverriding(current)}
          >
            {t("overrideRate")}
          </Button>
        )}
        {current.capabilities.canSetBilling && (
          <Button
            size="xs"
            h={40}
            variant="default"
            aria-label={t("setBillingFor", { description: lineName(current) })}
            onClick={() => setPricing(current)}
          >
            {t("setBilling")}
          </Button>
        )}
        {current.capabilities.canMarkInvoiced && (
          <Button
            size="xs"
            h={40}
            variant="default"
            aria-label={t("markInvoicedFor", { description: lineName(current) })}
            onClick={() => setInvoicing(current)}
          >
            {t("markInvoiced")}
          </Button>
        )}
        {current.capabilities.canUndoInvoiced && (
          <Button
            size="xs"
            h={40}
            variant="default"
            color="red"
            loading={undoInvoiced.isPending}
            aria-label={t("undoInvoicedFor", { description: lineName(current) })}
            onClick={() => undoInvoiced.confirm(current)}
          >
            {t("undoInvoiced")}
          </Button>
        )}
      </Group>
    );
  };

  const decidable: Claim["capabilities"] = claim.capabilities;

  return (
    <Stack>
      {refusals.length > 0 && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotDecideClaim")}>
          <RefusalList messages={refusals} />
        </Alert>
      )}

      <ClaimDetails
        claim={claim}
        timeZone={meta?.timeZone ?? "UTC"}
        withLineDetails
        lineActions={lineActions}
        lineRefusals={lineRefusals}
      />

      <Group wrap="wrap">
        {decidable.canApprove && (
          <Button loading={approve.isPending} onClick={() => approve.mutate()}>
            {t("approve")}
          </Button>
        )}
        {decidable.canApprove && (
          <Button color="red" variant="light" onClick={() => setRejecting(true)}>
            {t("reject")}
          </Button>
        )}
        {decidable.canUnapprove && (
          <Button variant="default" loading={unapprove.isPending} onClick={() => unapprove.mutate()}>
            {t("unapprove")}
          </Button>
        )}
      </Group>

      <RejectModal
        units={rejecting ? { claimIds: [claimId] } : null}
        onClose={() => setRejecting(false)}
        onRejected={() => {
          setRejecting(false);
          onClose();
        }}
      />
      <RateOverrideModal
        expense={overriding}
        revision={overriding?.revision}
        onClose={() => setOverriding(null)}
        onSaved={saved}
      />
      <BillingModal expense={pricing} revision={pricing?.revision} onClose={() => setPricing(null)} onSaved={saved} />
      <MarkInvoicedModal
        expense={invoicing}
        revision={invoicing?.revision}
        onClose={() => setInvoicing(null)}
        onSaved={saved}
      />
    </Stack>
  );
};
