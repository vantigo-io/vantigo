import { Alert, Anchor, Button, Group, Stack, Table, Text, Title } from "@mantine/core";
import { IconInfoCircle, IconPlus, IconReceiptOff } from "@tabler/icons-react";
import { useNavigate } from "@tanstack/react-router";
import { appUrl, PageSection } from "@vantigo/frontend-shell";
import { useState } from "react";
import type { InvoiceDocument } from "../api/invoices";
import type { InvoicePayment } from "../api/payments";
import "../i18n";
import { useInvoiceFormat } from "../lib/format";
import { PAYMENTS_ROUTE_PATH } from "../lib/routes";
import { RegisterPaymentModal } from "../pages/-register-payment-modal";
import { RemovePaymentModal } from "../pages/-remove-payment-modal";
import { UndoWriteOffModal, WriteOffModal } from "../pages/-write-off-modals";

export interface PaymentsCardProps {
  /** An issued invoice: a credit note takes no payments. */
  invoice: InvoiceDocument;
  /** meta's `canRegisterPayments` — `invoices:payments`, for both the registration and the removal. */
  canRegister: boolean;
  /** Today in Oslo, meta's. */
  today: string;
}

/** A state in which nothing is left to pay: no registration is offered (D10). */
const settled = new Set(["paid", "credited", "written_off"]);

/**
 * An issued invoice's payments (D2, D10): each registration with the day the
 * money arrived, the amount, the reference, the note and when it was
 * registered, and where it came from — by hand, or a line of an OCR giro or
 * camt.054 file, named, and linked to the Payments area for a caller who may
 * open it (invoices payments and reminders design D2, D22); a removed one
 * struck through with its reason, since the record is the point. Who
 * registered a payment is not shown: a bank line's may have no person. "Register payment" is offered to a caller with
 * `invoices:payments` while something is left to pay, and "Remove" on each
 * live registration. While the invoice is handed off to a collection agency
 * a payment is still registered here, and the card says to report one
 * received directly to the agency (invoices payments and reminders design D11).
 *
 * The write-off (accounting design A16, R4): "Write off" while something is
 * open, the standing write-off with its day, amount, reason and whether its
 * VAT was taken back, "Undo write-off" and "Register recovered payment" while
 * it stands, an undone one struck through with its reason, and a recovered
 * payment named as one in the list — all under `invoices:payments`.
 */
export const PaymentsCard = ({ invoice, canRegister, today }: PaymentsCardProps) => {
  const { t, money, date, dateTime } = useInvoiceFormat();
  const navigate = useNavigate() as (options: unknown) => void;
  const [registering, setRegistering] = useState(false);
  const [removing, setRemoving] = useState<InvoicePayment | null>(null);
  const [writingOff, setWritingOff] = useState(false);
  const [undoing, setUndoing] = useState(false);
  const [recovering, setRecovering] = useState(false);
  const payments = invoice.payments ?? [];
  const writeOffs = invoice.writeOffs ?? [];
  const standing = writeOffs.find((w) => !w.undoneAt);
  const canWriteOff = canRegister && !standing && (invoice.openAmount ?? 0) > 0;
  const handoff = invoice.handoff && !invoice.handoff.withdrawnOn ? invoice.handoff : undefined;
  return (
    <PageSection data-testid="payments-card">
      <Stack gap="xs">
        <Group justify="space-between">
          <Title order={4}>{t("payments")}</Title>
          <Group gap="xs">
            {canWriteOff && (
              <Button
                size="xs"
                variant="default"
                leftSection={<IconReceiptOff size={14} />}
                onClick={() => setWritingOff(true)}
              >
                {t("writeOff.button")}
              </Button>
            )}
            {canRegister && !settled.has(invoice.state) && (
              <Button size="xs" leftSection={<IconPlus size={14} />} onClick={() => setRegistering(true)}>
                {t("registerPayment")}
              </Button>
            )}
          </Group>
        </Group>
        {writeOffs.map((w) => {
          const undone = Boolean(w.undoneAt);
          return (
            <Alert
              key={w.id}
              color={undone ? "gray" : "orange"}
              icon={<IconReceiptOff size={16} />}
              data-testid={`write-off-${w.id}`}
              data-undone={undone ? "true" : undefined}
            >
              <Stack gap={4}>
                <Text size="sm" style={undone ? { textDecoration: "line-through" } : undefined}>
                  {t("writeOff.standing", {
                    date: date(w.writtenOffOn),
                    amount: money(w.amount, w.currency),
                    reason: t(`writeOff.reason.${w.reason}`),
                  })}
                  {w.vatCorrection ? ` · ${t("writeOff.vatCorrected")}` : ""}
                </Text>
                {w.recoveredAmount > 0 && (
                  <Text size="sm">
                    {t("writeOff.recoveredSoFar", { amount: money(w.recoveredAmount, w.currency) })}
                  </Text>
                )}
                {undone && <Text size="sm">{t("writeOff.undoneBecause", { reason: w.undoReason ?? "" })}</Text>}
                {!undone && canRegister && (
                  <Group gap="xs">
                    {w.recoveredAmount < w.amount && (
                      <Button size="xs" variant="light" onClick={() => setRecovering(true)}>
                        {t("writeOff.registerRecovered")}
                      </Button>
                    )}
                    <Button size="xs" variant="subtle" onClick={() => setUndoing(true)}>
                      {t("writeOff.undo")}
                    </Button>
                  </Group>
                )}
              </Stack>
            </Alert>
          );
        })}
        {handoff && (
          <Alert color="grape" icon={<IconInfoCircle size={16} />} data-testid="report-to-agency">
            {t("handoff.reportPayments", { agency: handoff.agency })}
          </Alert>
        )}
        {payments.length === 0 ? (
          <Text size="sm" c="dimmed">
            {t("noPayments")}
          </Text>
        ) : (
          <Table.ScrollContainer minWidth={640}>
            <Table>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("paidOn")}</Table.Th>
                  <Table.Th ta="right">{t("paymentAmount")}</Table.Th>
                  <Table.Th>{t("paymentSource")}</Table.Th>
                  <Table.Th>{t("paymentReference")}</Table.Th>
                  <Table.Th>{t("note")}</Table.Th>
                  <Table.Th>{t("registeredAt")}</Table.Th>
                  {canRegister && <Table.Th />}
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {payments.map((p) => {
                  const removed = Boolean(p.removedAt);
                  // A removed registration is struck through, its reason beside it.
                  const struck = removed ? { textDecoration: "line-through" } : undefined;
                  return (
                    <Table.Tr key={p.id} data-removed={removed ? "true" : undefined} c={removed ? "dimmed" : undefined}>
                      <Table.Td style={struck}>{date(p.paidOn)}</Table.Td>
                      <Table.Td ta="right" style={struck}>
                        {money(p.amount, p.currency)}
                      </Table.Td>
                      <Table.Td data-testid={`payment-source-${p.id}`}>
                        <Text size="sm" style={struck}>
                          {p.kind === "recovered" ? t("writeOff.recoveredKind") : t(`paymentSource.${p.source}`)}
                        </Text>
                        {p.bankTransactionId !== undefined &&
                          (canRegister ? (
                            <Anchor
                              size="xs"
                              href={appUrl(PAYMENTS_ROUTE_PATH)}
                              onClick={(event) => {
                                if (event.metaKey || event.ctrlKey || event.shiftKey || event.button !== 0) return;
                                event.preventDefault();
                                navigate({ to: PAYMENTS_ROUTE_PATH });
                              }}
                            >
                              {t("paymentBankLine", { id: p.bankTransactionId })}
                            </Anchor>
                          ) : (
                            <Text size="xs" c="dimmed">
                              {t("paymentBankLine", { id: p.bankTransactionId })}
                            </Text>
                          ))}
                      </Table.Td>
                      <Table.Td style={struck}>{p.reference}</Table.Td>
                      <Table.Td>
                        <Text size="sm" style={struck}>
                          {p.note}
                        </Text>
                        {removed && <Text size="sm">{t("removedBecause", { reason: p.removalReason ?? "" })}</Text>}
                      </Table.Td>
                      <Table.Td style={struck}>{dateTime(p.registeredAt)}</Table.Td>
                      {canRegister && (
                        <Table.Td ta="right">
                          {!removed && (
                            <Button
                              size="xs"
                              variant="subtle"
                              color="red"
                              // "Remove" alone says not which: each button names its payment.
                              aria-label={t("removePaymentOf", {
                                date: date(p.paidOn),
                                amount: money(p.amount, p.currency),
                              })}
                              onClick={() => setRemoving(p)}
                            >
                              {t("removePaymentButton")}
                            </Button>
                          )}
                        </Table.Td>
                      )}
                    </Table.Tr>
                  );
                })}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}
      </Stack>
      {registering && <RegisterPaymentModal invoice={invoice} today={today} onClose={() => setRegistering(false)} />}
      {removing && <RemovePaymentModal invoice={invoice} payment={removing} onClose={() => setRemoving(null)} />}
      {writingOff && <WriteOffModal invoice={invoice} today={today} onClose={() => setWritingOff(false)} />}
      {undoing && <UndoWriteOffModal invoice={invoice} onClose={() => setUndoing(false)} />}
      {recovering && (
        <RegisterPaymentModal invoice={invoice} today={today} recovered onClose={() => setRecovering(false)} />
      )}
    </PageSection>
  );
};
