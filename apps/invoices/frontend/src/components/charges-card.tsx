import { Button, Card, Group, Stack, Table, Text, Title } from "@mantine/core";
import { IconPlus } from "@tabler/icons-react";
import { useState } from "react";
import { type ChargePayment, removeChargePayment } from "../api/charges";
import type { InvoiceDocument } from "../api/invoices";
import { invoicesCatalog } from "../i18n";
import { useWho } from "../lib/bank";
import { useInvoiceFormat } from "../lib/format";
import { letterName, waivableCharges } from "../lib/reminders";
import { ChargePaymentModal } from "../pages/-charge-payment-modal";
import { WaiveModal } from "../pages/-waive-modal";
import { ReasonModal } from "./reason-modal";

export interface ChargesCardProps {
  /** An issued invoice. */
  invoice: InvoiceDocument;
  /** meta's `canRegisterPayments` — `invoices:payments`, for a charge payment, its removal and a waiver. */
  canAct: boolean;
  /** Today in Oslo, meta's. */
  today: string;
  currentUserId?: string;
}

/**
 * An invoice's reminder charges, apart from its principal (invoices payments
 * and reminders design D9, D22): what its letters claimed, what was waived and
 * paid, what is outstanding, a refund due when a charge was paid and then
 * waived, and the late interest accrued to today — a figure, not a claim. Then
 * every charge payment, a removed one struck through, and every waiver, an
 * interest waiver as the amount it released. "Register a charge payment" is
 * offered with `invoices:payments` while charges are outstanding, "Waive"
 * while a sent letter claims one.
 */
export const ChargesCard = ({ invoice, canAct, today, currentUserId }: ChargesCardProps) => {
  const { t, money, date, dateTime } = useInvoiceFormat();
  const who = useWho(currentUserId);
  const [registering, setRegistering] = useState(false);
  const [waiving, setWaiving] = useState(false);
  const [removing, setRemoving] = useState<ChargePayment | null>(null);
  const charges = invoice.charges ?? { claimed: 0, waived: 0, paid: 0, outstanding: 0 };
  const payments = invoice.chargePayments ?? [];
  const waivers = invoice.waivers ?? [];
  const letters = invoice.reminders ?? [];
  const sequenceOf = (reminderId: number) => letters.find((r) => r.id === reminderId)?.sequence;
  const figure = (label: string, amount: number, testId: string, strong = false) => (
    <Table.Tr>
      <Table.Td fw={strong ? 700 : undefined}>{label}</Table.Td>
      <Table.Td ta="right" fw={strong ? 700 : undefined} data-testid={testId}>
        {money(amount, invoice.currency)}
      </Table.Td>
    </Table.Tr>
  );
  return (
    <Card withBorder data-testid="charges-card">
      <Stack gap="xs">
        <Group justify="space-between">
          <Title order={4}>{t("charges.title")}</Title>
          {canAct && (
            <Group gap="xs">
              {charges.outstanding > 0 && (
                <Button size="xs" leftSection={<IconPlus size={14} />} onClick={() => setRegistering(true)}>
                  {t("charges.registerPayment")}
                </Button>
              )}
              {waivableCharges(invoice).length > 0 && (
                <Button size="xs" variant="default" onClick={() => setWaiving(true)}>
                  {t("charges.waive.button")}
                </Button>
              )}
            </Group>
          )}
        </Group>
        <Text size="xs" c="dimmed">
          {t("charges.apart")}
        </Text>
        <Table withRowBorders={false} maw={420}>
          <Table.Tbody>
            {figure(t("charges.claimed"), charges.claimed, "charges-claimed")}
            {figure(t("charges.waivedAmount"), charges.waived, "charges-waived")}
            {figure(t("charges.paid"), charges.paid, "charges-paid")}
            {figure(t("charges.outstanding"), charges.outstanding, "charges-outstanding", true)}
            {charges.refundDue !== undefined && figure(t("charges.refundDue"), charges.refundDue, "charges-refund-due")}
          </Table.Tbody>
        </Table>
        {charges.interestToday !== undefined && (
          <Text size="sm" data-testid="interest-today">
            {t("charges.interestToday", { amount: money(charges.interestToday, invoice.currency) })}
          </Text>
        )}
        {payments.length > 0 && (
          <>
            <Title order={5}>{t("charges.payments")}</Title>
            <Table.ScrollContainer minWidth={560}>
              <Table>
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>{t("paidOn")}</Table.Th>
                    <Table.Th ta="right">{t("paymentAmount")}</Table.Th>
                    <Table.Th>{t("paymentSource")}</Table.Th>
                    <Table.Th>{t("paymentReference")}</Table.Th>
                    <Table.Th>{t("note")}</Table.Th>
                    {canAct && <Table.Th />}
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {payments.map((p) => {
                    const removed = Boolean(p.removedAt);
                    const struck = removed ? { textDecoration: "line-through" } : undefined;
                    return (
                      <Table.Tr
                        key={p.id}
                        data-removed={removed ? "true" : undefined}
                        c={removed ? "dimmed" : undefined}
                      >
                        <Table.Td style={struck}>{date(p.paidOn)}</Table.Td>
                        <Table.Td ta="right" style={struck}>
                          {money(p.amount, p.currency)}
                        </Table.Td>
                        <Table.Td style={struck}>{t(`paymentSource.${p.source}`)}</Table.Td>
                        <Table.Td style={struck}>{p.reference}</Table.Td>
                        <Table.Td>
                          <Text size="sm" style={struck}>
                            {p.note}
                          </Text>
                          {removed && <Text size="sm">{t("removedBecause", { reason: p.removalReason ?? "" })}</Text>}
                        </Table.Td>
                        {canAct && (
                          <Table.Td ta="right">
                            {!removed && (
                              <Button
                                size="xs"
                                variant="subtle"
                                color="red"
                                aria-label={t("charges.removePaymentOf", {
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
          </>
        )}
        {waivers.length > 0 && (
          <>
            <Title order={5}>{t("charges.waivers")}</Title>
            <Table.ScrollContainer minWidth={560}>
              <Table>
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>{t("charges.col.charge")}</Table.Th>
                    <Table.Th ta="right">{t("paymentAmount")}</Table.Th>
                    <Table.Th>{t("charges.col.reason")}</Table.Th>
                    <Table.Th>{t("charges.col.waived")}</Table.Th>
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {waivers.map((w) => {
                    const sequence = sequenceOf(w.reminderId);
                    const letter = sequence === undefined ? String(w.reminderId) : letterName(t, sequence);
                    return (
                      <Table.Tr key={w.id} data-waiver={w.id}>
                        <Table.Td>
                          {w.kind === "interest" && w.interestThrough
                            ? t("charges.waiver.interest", { letter, date: date(w.interestThrough) })
                            : t(`charges.waiver.${w.kind}`, { letter })}
                        </Table.Td>
                        <Table.Td ta="right">{money(w.amount, invoice.currency)}</Table.Td>
                        <Table.Td>
                          <Text size="sm">
                            {`charges.reason.${w.reason}` in invoicesCatalog.en
                              ? t(`charges.reason.${w.reason}`)
                              : w.reason}
                          </Text>
                          {w.note && (
                            <Text size="xs" c="dimmed">
                              {w.note}
                            </Text>
                          )}
                        </Table.Td>
                        <Table.Td>{t("charges.waivedBy", { at: dateTime(w.waivedAt), who: who(w.waivedBy) })}</Table.Td>
                      </Table.Tr>
                    );
                  })}
                </Table.Tbody>
              </Table>
            </Table.ScrollContainer>
          </>
        )}
      </Stack>
      {registering && <ChargePaymentModal invoice={invoice} today={today} onClose={() => setRegistering(false)} />}
      {waiving && <WaiveModal invoice={invoice} onClose={() => setWaiving(false)} />}
      {removing && (
        <ReasonModal
          title={t("charges.removePayment")}
          summary={`${date(removing.paidOn)} · ${money(removing.amount, removing.currency)}`}
          hint={t("removePaymentHint")}
          submitLabel={t("charges.removePayment")}
          doneMessage={t("charges.paymentRemoved")}
          errorTitle={t("charges.couldNotRemove")}
          onSubmit={(reason) => removeChargePayment(invoice.id, removing.id, reason)}
          onClose={() => setRemoving(null)}
        />
      )}
    </Card>
  );
};
