import { Button, Card, Group, Stack, Table, Text, Title } from "@mantine/core";
import { IconPlus } from "@tabler/icons-react";
import { useState } from "react";
import { type ManualDelivery, removeDelivery } from "../api/charges";
import type { InvoiceDocument } from "../api/invoices";
import type { InvoiceDelivery } from "../api/send";
import "../i18n";
import { useWho } from "../lib/bank";
import { useInvoiceFormat } from "../lib/format";
import { ManualDeliveryModal } from "../pages/-manual-delivery-modal";
import { ReasonModal } from "./reason-modal";

export interface DeliveriesCardProps {
  deliveries: InvoiceDelivery[];
  /**
   * An issued invoice, whose deliveries recorded by hand are listed below the
   * e-mails (invoices payments and reminders design D8). Absent on a credit
   * note, which is never reminded of.
   */
  invoice?: InvoiceDocument;
  /** meta's `canIssue` — `invoices:issue`, which recording a delivery and removing one need. */
  canIssue?: boolean;
  /** Today in Oslo, meta's. */
  today?: string;
  currentUserId?: string;
}

/**
 * Every e-mail that handed an issued document over (D4, D10): when it went,
 * to whom and under which subject. The address is the server's to give — only
 * a caller with `invoices:issue` gets it — so a reader sees no address column
 * at all; one the anonymisation blanked says "(anonymised)". On an invoice,
 * the deliveries recorded by hand follow — handed over or posted, by whom, a
 * removed one struck through with its reason — with "Record a delivery" and
 * each record's removal for a caller with `invoices:issue`; a removal a
 * reminder's charge rests on is refused, in words.
 */
export const DeliveriesCard = ({
  deliveries,
  invoice,
  canIssue = false,
  today,
  currentUserId,
}: DeliveriesCardProps) => {
  const { t, date, dateTime } = useInvoiceFormat();
  const who = useWho(currentUserId);
  const [recording, setRecording] = useState(false);
  const [removing, setRemoving] = useState<ManualDelivery | null>(null);
  const withAddress = deliveries.some((d) => d.recipient !== undefined);
  const manual = invoice?.manualDeliveries ?? [];
  return (
    <Card withBorder data-testid="deliveries-card">
      <Stack gap="xs">
        <Group justify="space-between">
          <Title order={4}>{t("deliveries")}</Title>
          {invoice && canIssue && today && (
            <Button size="xs" variant="default" leftSection={<IconPlus size={14} />} onClick={() => setRecording(true)}>
              {t("delivery.record")}
            </Button>
          )}
        </Group>
        {deliveries.length === 0 ? (
          <Text size="sm" c="dimmed">
            {t("notSentYet")}
          </Text>
        ) : (
          <Table.ScrollContainer minWidth={480}>
            <Table>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("sentAt")}</Table.Th>
                  {withAddress && <Table.Th>{t("sentToColumn")}</Table.Th>}
                  <Table.Th>{t("subject")}</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {deliveries.map((d) => (
                  <Table.Tr key={d.id}>
                    <Table.Td>{dateTime(d.sentAt)}</Table.Td>
                    {withAddress && (
                      <Table.Td>
                        {d.recipient === "" ? (
                          <Text size="sm" c="dimmed" component="span">
                            {t("anonymised")}
                          </Text>
                        ) : (
                          d.recipient
                        )}
                      </Table.Td>
                    )}
                    <Table.Td>{d.subject}</Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}
        {manual.length > 0 && (
          <>
            <Title order={5}>{t("delivery.byHand")}</Title>
            <Table.ScrollContainer minWidth={480}>
              <Table data-testid="manual-deliveries">
                <Table.Tbody>
                  {manual.map((d) => {
                    const removed = Boolean(d.removedAt);
                    const struck = removed ? { textDecoration: "line-through" } : undefined;
                    return (
                      <Table.Tr
                        key={d.id}
                        data-removed={removed ? "true" : undefined}
                        c={removed ? "dimmed" : undefined}
                      >
                        <Table.Td style={struck}>
                          {t(`delivery.kindOn.${d.kind}`, { date: date(d.deliveredOn) })}
                        </Table.Td>
                        <Table.Td>
                          <Text size="sm" style={struck}>
                            {d.note}
                          </Text>
                          {removed && <Text size="sm">{t("removedBecause", { reason: d.removalReason ?? "" })}</Text>}
                        </Table.Td>
                        <Table.Td style={struck}>
                          {t("delivery.recordedBy", { at: dateTime(d.recordedAt), who: who(d.recordedByUserId) })}
                        </Table.Td>
                        {canIssue && (
                          <Table.Td ta="right">
                            {!removed && (
                              <Button
                                size="xs"
                                variant="subtle"
                                color="red"
                                aria-label={t("delivery.removeOf", {
                                  kind: t(`delivery.kind.${d.kind}`),
                                  date: date(d.deliveredOn),
                                })}
                                onClick={() => setRemoving(d)}
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
      </Stack>
      {recording && invoice && today && (
        <ManualDeliveryModal invoice={invoice} today={today} onClose={() => setRecording(false)} />
      )}
      {removing && invoice && (
        <ReasonModal
          title={t("delivery.remove")}
          summary={t(`delivery.kindOn.${removing.kind}`, { date: date(removing.deliveredOn) })}
          hint={t("delivery.removeHint")}
          submitLabel={t("delivery.remove")}
          doneMessage={t("delivery.removed")}
          errorTitle={t("delivery.couldNotRemove")}
          onSubmit={(reason) => removeDelivery(invoice.id, removing.id, reason)}
          onClose={() => setRemoving(null)}
        />
      )}
    </Card>
  );
};
