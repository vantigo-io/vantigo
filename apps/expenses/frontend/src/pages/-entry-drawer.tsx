import { Button, Drawer, Group, Stack } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { approveExpenses, unapproveExpenses } from "../api/approvals";
import type { Expense, FlowResult } from "../api/entries";
import { EXPENSES_QUERY_KEY } from "../api/request";
import { EntryDetails } from "../components/entry-details";
import { RefusalList } from "../components/refusal-list";
import "../i18n";
import { refusalMessages } from "../lib/errors";
import { BillingModal } from "./-billing-modal";
import { RateOverrideModal } from "./-rate-override-modal";
import { RejectModal } from "./-reject-modal";

export interface EntryDrawerProps {
  /** The expense being looked at, or null when the drawer is closed. */
  expense: Expense | null;
  onClose: () => void;
}

/**
 * One expense in full, with the actions its own `capabilities` allow and no
 * others — approve, reject, take an approval back, replace the mileage rate,
 * and price it from the project's side.
 *
 * The revision every guarded write carries is the one the drawer was
 * **opened** at, and it moves on only to what a write answered. A list
 * refreshing underneath never changes it, so two people editing the same
 * expense still get the 409 that is the point of the guard.
 */
export const EntryDrawer = ({ expense, onClose }: EntryDrawerProps) => {
  return (
    <Drawer opened={expense !== null} onClose={onClose} position="right" size="lg" title={expense?.description ?? ""}>
      {expense && <EntryActions key={expense.id} expense={expense} onClose={onClose} />}
    </Drawer>
  );
};

const EntryActions = ({ expense, onClose }: { expense: Expense; onClose: () => void }) => {
  const { t } = useI18n("expenses");
  const queryClient = useQueryClient();
  // The expense as this drawer knows it: the one it opened on, then whatever
  // a write here answered with. It is never a refetch — see the revision note.
  const [current, setCurrent] = useState(expense);
  const [revision, setRevision] = useState(expense.revision);
  const [refusals, setRefusals] = useState<string[]>([]);
  const [rejecting, setRejecting] = useState(false);
  const [overriding, setOverriding] = useState<Expense | null>(null);
  const [pricing, setPricing] = useState<Expense | null>(null);

  const saved = (next: Expense) => {
    setCurrent(next);
    setRevision(next.revision);
    setOverriding(null);
    setPricing(null);
  };

  /** Approve and unapprove differ only in the call and the words; the handling is one shape. */
  const decided = (title: string, failure: string) => ({
    onSuccess: async ({ entries: [moved] }: FlowResult) => {
      setRefusals([]);
      await queryClient.invalidateQueries({ queryKey: [EXPENSES_QUERY_KEY] });
      notifications.show({ color: "teal", title, message: current.description });
      if (moved) saved(moved);
      onClose();
    },
    onError: (error: Error) => {
      setRefusals(refusalMessages(error));
      notifications.show({ color: "red", title: failure, message: "" });
    },
  });

  const approve = useMutation({
    mutationFn: () => approveExpenses([current.id]),
    ...decided(t("expensesApproved"), t("couldNotApprove")),
  });
  const unapprove = useMutation({
    mutationFn: () => unapproveExpenses([current.id]),
    ...decided(t("expensesUnapproved"), t("couldNotUnapprove")),
  });

  return (
    <Stack>
      <RefusalList messages={refusals} />
      <EntryDetails expense={current} />

      <Group wrap="wrap">
        {current.capabilities.canApprove && (
          <Button loading={approve.isPending} onClick={() => approve.mutate()}>
            {t("approve")}
          </Button>
        )}
        {current.capabilities.canApprove && (
          <Button color="red" variant="light" onClick={() => setRejecting(true)}>
            {t("reject")}
          </Button>
        )}
        {current.capabilities.canUnapprove && (
          <Button variant="default" loading={unapprove.isPending} onClick={() => unapprove.mutate()}>
            {t("unapprove")}
          </Button>
        )}
        {current.capabilities.canOverrideRate && (
          <Button variant="default" onClick={() => setOverriding(current)}>
            {t("overrideRate")}
          </Button>
        )}
        {current.capabilities.canSetBilling && (
          <Button variant="default" onClick={() => setPricing(current)}>
            {t("setBilling")}
          </Button>
        )}
      </Group>

      <RejectModal
        units={rejecting ? { entryIds: [current.id] } : null}
        onClose={() => setRejecting(false)}
        onRejected={() => {
          setRejecting(false);
          onClose();
        }}
      />
      <RateOverrideModal expense={overriding} revision={revision} onClose={() => setOverriding(null)} onSaved={saved} />
      <BillingModal expense={pricing} revision={revision} onClose={() => setPricing(null)} onSaved={saved} />
    </Stack>
  );
};
