import { Alert, Button, Checkbox, Group, Modal, Radio, Stack, Text, Textarea } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton } from "@vantigo/frontend-shell";
import { useState } from "react";
import { type BankTransaction, handleReversal, MAX_PAGES, reversalLinesQueryOptions } from "../api/bank";
import { ApiValidationError, INVOICES_QUERY_KEY } from "../api/request";
import "../i18n";
import { fieldRefusals, refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";

export interface ReversalDialogProps {
  /** An exception queued `reversal`. */
  line: BankTransaction;
  /** The installation's currency, meta's. */
  currency: string;
  onClose: () => void;
}

/** One payment a reversal may take back, and the bank line it came from. */
interface Candidate {
  invoiceId: number;
  paymentId: number;
  number: number;
  amount: number;
  lineRef: string;
  bookedOn: string;
}

/**
 * The payments a reversal may take back, computed here (the server links
 * nothing by itself): the live payments of the bank lines of the same account
 * and amount — matched or applied — booked on or before the reversal. The
 * server filters by account and amount already; they are checked again here,
 * so a line of another account or amount is never offered.
 */
const candidatesOf = (reversal: BankTransaction, lines: BankTransaction[]): Candidate[] =>
  lines
    .filter(
      (l) =>
        l.id !== reversal.id &&
        l.direction === "credit" &&
        l.account === reversal.account &&
        Math.round(l.amount * 100) === Math.round(Math.abs(reversal.amount) * 100),
    )
    .flatMap((l) =>
      l.applied
        .filter((a) => a.kind === "payment" && !a.removed)
        .map((a) => ({
          invoiceId: a.invoiceId,
          paymentId: a.id,
          number: a.number ?? a.invoiceId,
          amount: a.amount,
          lineRef: l.lineRef,
          bookedOn: l.bookedOn,
        })),
    );

/**
 * Handles a reversal (D5): the payments it takes back — chosen from the
 * candidates of the same account and amount — or "no payment" with a note
 * saying why. A removed payment's line is never applied again, and a reversal
 * that names the wrong payment cannot be undone, which the dialog says. Every
 * refusal, `reversal_payment_required` among them, is said in words.
 */
export const ReversalDialog = ({ line, currency, onClose }: ReversalDialogProps) => {
  const { t, money, date, dateTime } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const lines = useQuery(reversalLinesQueryOptions(line));
  const [mode, setMode] = useState<"payments" | "none">("payments");
  const [chosen, setChosen] = useState<Set<number>>(new Set());
  const [note, setNote] = useState("");
  const [refusal, setRefusal] = useState<string | null>(null);
  const amount = (value: number) => money(value, currency);

  const candidates = candidatesOf(line, lines.data?.data ?? []);

  const submit = useMutation({
    mutationFn: () =>
      handleReversal(
        line.id,
        mode === "none"
          ? { noPayment: true, ...(note.trim() ? { note: note.trim() } : {}) }
          : {
              removePayments: candidates
                .filter((c) => chosen.has(c.paymentId))
                .map((c) => ({ invoiceId: c.invoiceId, paymentId: c.paymentId })),
              ...(note.trim() ? { note: note.trim() } : {}),
            },
      ),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      notifications.show({ color: "green", message: t("bank.done.reversal") });
      onClose();
    },
    onError: (error) =>
      setRefusal(
        error instanceof ApiValidationError
          ? fieldRefusals(error, t, () => false, "reversal").elsewhere.join(" ")
          : refusalMessage(error, t, date, amount, dateTime),
      ),
  });

  return (
    <Modal opened onClose={onClose} size="lg" title={t("bank.reversal.title", { ref: line.lineRef })}>
      <Stack>
        <Text size="sm">
          {t("bank.reversal.explain", { amount: amount(Math.abs(line.amount)), date: date(line.bookedOn) })}
        </Text>
        <Radio.Group
          value={mode}
          onChange={(value) => {
            setMode(value === "none" ? "none" : "payments");
            setRefusal(null);
          }}
        >
          <Stack gap="xs">
            <Radio value="payments" label={t("bank.reversal.payments")} />
            {mode === "payments" &&
              (lines.isPending ? (
                <ContentSkeleton rows={2} rowHeight={28} />
              ) : lines.isError ? (
                // A read that failed is never "no payment": the person is told it failed.
                <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("bank.reversal.couldNotLoad")} ml="xl">
                  {refusalMessage(lines.error, t, date, amount, dateTime)}
                </Alert>
              ) : candidates.length === 0 ? (
                <Text size="sm" c="dimmed" pl="xl">
                  {t("bank.reversal.noCandidates")}
                </Text>
              ) : (
                <Stack gap={4} pl="xl">
                  {candidates.map((c) => (
                    <Checkbox
                      key={c.paymentId}
                      label={t("bank.reversal.candidate", {
                        number: c.number,
                        amount: amount(c.amount),
                        ref: c.lineRef,
                        date: date(c.bookedOn),
                      })}
                      checked={chosen.has(c.paymentId)}
                      onChange={(event) => {
                        const on = event.currentTarget.checked;
                        setRefusal(null);
                        setChosen((current) => {
                          const next = new Set(current);
                          if (on) next.add(c.paymentId);
                          else next.delete(c.paymentId);
                          return next;
                        });
                      }}
                    />
                  ))}
                  <Text size="xs" c="dimmed">
                    {t("bank.reversal.candidatesNote")}
                  </Text>
                  {lines.data?.truncated && (
                    <Text size="xs" c="orange">
                      {t("bank.reversal.truncated", { count: MAX_PAGES * 100 })}
                    </Text>
                  )}
                </Stack>
              ))}
            <Radio value="none" label={t("bank.reversal.noPayment")} />
          </Stack>
        </Radio.Group>
        <Textarea
          label={t("note")}
          maxLength={500}
          value={note}
          onChange={(e) => {
            setNote(e.currentTarget.value);
            setRefusal(null);
          }}
        />
        <Text size="xs" c="dimmed">
          {t("bank.reversal.irreversible")}
        </Text>
        {refusal && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("bank.couldNotAct")}>
            {refusal}
          </Alert>
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button color="red" disabled={submit.isPending} loading={submit.isPending} onClick={() => submit.mutate()}>
            {t("bank.reversal.submit")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
