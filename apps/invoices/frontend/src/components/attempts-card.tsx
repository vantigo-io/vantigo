import { Badge, Button, Card, Stack, Table, Text, Title } from "@mantine/core";
import { useState } from "react";
import { FINISHED_ATTEMPT_STATES, type PaymentAttempt } from "../api/attempts";
import type { InvoiceDocument } from "../api/invoices";
import "../i18n";
import { useInvoiceFormat } from "../lib/format";
import { AbandonModal } from "../pages/-abandon-modal";

export interface AttemptsCardProps {
  invoice: InvoiceDocument;
  /** meta's `canManage` — `invoices:manage`, which abandoning a stuck payment needs. */
  canManage: boolean;
}

const stateColours: Partial<Record<PaymentAttempt["state"], string>> = {
  captured: "green",
  capture_unknown: "orange",
  capture_failed: "red",
  failed: "red",
  abandoned: "gray",
};

/**
 * An invoice's Vipps payments (invoices payments and reminders design D16,
 * D22): each attempt's reference at Vipps, the way it was paid, its state in
 * words, its amount and what Vipps reported — approved, taken, released and
 * refunded in the portal — with what it holds on the invoice while a capture
 * is decided; an abandoned one says who abandoned it, when and why.
 * "Abandon" is offered to `invoices:manage` on an attempt that is not over,
 * through the confirmation of a check in the Vipps portal. Shown only when
 * the invoice has any.
 */
export const AttemptsCard = ({ invoice, canManage }: AttemptsCardProps) => {
  const { t, money, dateTime } = useInvoiceFormat();
  const [abandoning, setAbandoning] = useState<PaymentAttempt | null>(null);
  const attempts = invoice.paymentAttempts ?? [];
  if (attempts.length === 0) return null;
  const amount = (value: number) => money(value, invoice.currency);
  return (
    <Card withBorder data-testid="attempts-card">
      <Stack gap="xs">
        <Title order={4}>{t("attempts.title")}</Title>
        <Table.ScrollContainer minWidth={720}>
          <Table>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t("attempts.reference")}</Table.Th>
                <Table.Th>{t("attempts.flow")}</Table.Th>
                <Table.Th>{t("attempts.state")}</Table.Th>
                <Table.Th ta="right">{t("attempts.amount")}</Table.Th>
                <Table.Th>{t("attempts.atVipps")}</Table.Th>
                <Table.Th>{t("attempts.startedAt")}</Table.Th>
                {canManage && <Table.Th />}
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {attempts.map((a) => (
                <Table.Tr key={a.id}>
                  <Table.Td>{a.reference}</Table.Td>
                  <Table.Td>{t(`attempts.flow.${a.flow}`)}</Table.Td>
                  <Table.Td>
                    <Badge variant="light" color={stateColours[a.state] ?? "blue"}>
                      {t(`attempt.state.${a.state}`)}
                    </Badge>
                  </Table.Td>
                  <Table.Td ta="right">{amount(a.amount)}</Table.Td>
                  <Table.Td>
                    <Stack gap={0}>
                      {a.authorizedAmount !== undefined && (
                        <Text size="xs">{t("attempts.authorized", { amount: amount(a.authorizedAmount) })}</Text>
                      )}
                      {a.capturedAmount !== undefined && a.capturedAmount > 0 && (
                        <Text size="xs">{t("attempts.captured", { amount: amount(a.capturedAmount) })}</Text>
                      )}
                      {a.cancelledAmount !== undefined && a.cancelledAmount > 0 && (
                        <Text size="xs">{t("attempts.cancelled", { amount: amount(a.cancelledAmount) })}</Text>
                      )}
                      {a.refundedAmount !== undefined && a.refundedAmount > 0 && (
                        <Text size="xs" c="orange">
                          {t("attempts.refunded", { amount: amount(a.refundedAmount) })}
                        </Text>
                      )}
                      {a.reservedAmount !== undefined && a.reservedAmount > 0 && (
                        <Text size="xs">{t("attempts.reserved", { amount: amount(a.reservedAmount) })}</Text>
                      )}
                      {a.releasePending && <Text size="xs">{t("attempts.releasePending")}</Text>}
                      {a.abandonedAt && (
                        <Text size="xs">
                          {t("attempts.abandonedBecause", {
                            at: dateTime(a.abandonedAt),
                            reason: a.abandonReason ?? "",
                          })}
                        </Text>
                      )}
                    </Stack>
                  </Table.Td>
                  <Table.Td>{dateTime(a.createdAt)}</Table.Td>
                  {canManage && (
                    <Table.Td ta="right">
                      {!FINISHED_ATTEMPT_STATES.includes(a.state) && (
                        <Button
                          size="xs"
                          variant="subtle"
                          color="red"
                          aria-label={t("attempts.abandonOf", { reference: a.reference })}
                          onClick={() => setAbandoning(a)}
                        >
                          {t("attempts.abandon")}
                        </Button>
                      )}
                    </Table.Td>
                  )}
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        </Table.ScrollContainer>
      </Stack>
      {abandoning && (
        <AbandonModal attempt={abandoning} currency={invoice.currency} onClose={() => setAbandoning(null)} />
      )}
    </Card>
  );
};
