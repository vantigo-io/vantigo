import { Badge, Button, Card, Group, Stack, Text, Title } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconDownload } from "@tabler/icons-react";
import { useMutation } from "@tanstack/react-query";
import { useState } from "react";
import { downloadCollectionCsv, type LetterLeft } from "../api/collection";
import { isSessionExpired, saveCsv } from "../api/export";
import type { InvoiceDocument } from "../api/invoices";
import "../i18n";
import { useWho } from "../lib/bank";
import { refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";
import { HandoffModal, WithdrawHandoffModal } from "../pages/-handoff-modal";
import { ManualDeliveryModal } from "../pages/-manual-delivery-modal";

export interface HandoffCardProps {
  invoice: InvoiceDocument;
  /** meta's `canRegisterPayments` — `invoices:payments`, which the hand-off, its withdrawal and the export need. */
  canAct: boolean;
  /** meta's `canIssue` — `invoices:issue`, which recording a delivery needs. */
  canIssue: boolean;
  /** Today in Oslo, meta's. */
  today: string;
  currentUserId?: string;
  onLettersLeft: (letters: LetterLeft[]) => void;
}

/** A state in which nothing is left to pay: the hand-off is refused `invoice_settled`, so it is not offered. */
const settled = new Set(["paid", "credited"]);

/**
 * The invoice's hand-off to a collection agency (invoices payments and
 * reminders design D11, D22): a live one — the agency, the day, its case
 * number, by whom — with "Withdraw the hand-off", or the last one and why it
 * came back; "Hand off to collection" otherwise, never offered on a settled
 * invoice. "Export for the agency" downloads this invoice's collection file,
 * the principal apart from the charges and what was waived — the file the
 * hand-off is made with, so it is offered before as well as after. All need
 * `invoices:payments`.
 */
export const HandoffCard = ({ invoice, canAct, canIssue, today, currentUserId, onLettersLeft }: HandoffCardProps) => {
  const { t, date } = useInvoiceFormat();
  const who = useWho(currentUserId);
  const [modal, setModal] = useState<"handoff" | "withdraw" | "delivery" | null>(null);
  const handoff = invoice.handoff;
  const live = handoff !== undefined && !handoff.withdrawnOn;
  const exportCsv = useMutation({
    mutationFn: () => downloadCollectionCsv(invoice.id),
    onSuccess: saveCsv,
    onError: (error) => {
      if (isSessionExpired(error)) return;
      notifications.show({ color: "red", title: t("handoff.couldNotExport"), message: refusalMessage(error, t, date) });
    },
  });
  return (
    <Card withBorder data-testid="handoff-card">
      <Stack gap="xs">
        <Group justify="space-between">
          <Title order={4}>{t("handoff.title")}</Title>
          {canAct && (
            <Group gap="xs">
              <Button
                size="xs"
                variant="subtle"
                leftSection={<IconDownload size={14} />}
                loading={exportCsv.isPending}
                onClick={() => exportCsv.mutate()}
              >
                {t("handoff.export")}
              </Button>
              {live ? (
                <Button size="xs" variant="default" onClick={() => setModal("withdraw")}>
                  {t("handoff.withdraw")}
                </Button>
              ) : (
                !settled.has(invoice.state) && (
                  <Button size="xs" variant="default" onClick={() => setModal("handoff")}>
                    {t("handoff.handOff")}
                  </Button>
                )
              )}
            </Group>
          )}
        </Group>
        {live && handoff && (
          <>
            <Group gap="xs">
              <Badge color="grape" variant="light">
                {t("handoff.handedOff")}
              </Badge>
              <Text size="sm">
                {t("handoff.handedTo", {
                  agency: handoff.agency,
                  date: date(handoff.handedOn),
                  who: who(handoff.createdBy),
                })}
              </Text>
            </Group>
            {handoff.agencyReference && (
              <Text size="sm">{t("handoff.referenceIs", { reference: handoff.agencyReference })}</Text>
            )}
            {handoff.note && <Text size="sm">{handoff.note}</Text>}
            <Text size="xs" c="dimmed">
              {t("handoff.whileHandedOff")}
            </Text>
          </>
        )}
        {!live && handoff?.withdrawnOn && (
          <Text size="sm">
            {t("handoff.withdrawnFrom", {
              agency: handoff.agency,
              date: date(handoff.withdrawnOn),
              reason: handoff.withdrawalReason ?? "",
            })}
          </Text>
        )}
        {!handoff && (
          <Text size="sm" c="dimmed">
            {t("handoff.none")}
          </Text>
        )}
      </Stack>
      {modal === "handoff" && (
        <HandoffModal
          invoice={invoice}
          today={today}
          canIssue={canIssue}
          onRecordDelivery={() => setModal("delivery")}
          onLettersLeft={onLettersLeft}
          onClose={() => setModal(null)}
        />
      )}
      {modal === "withdraw" && handoff && (
        <WithdrawHandoffModal
          invoice={invoice}
          handoff={handoff}
          today={today}
          onLettersLeft={onLettersLeft}
          onClose={() => setModal(null)}
        />
      )}
      {modal === "delivery" && <ManualDeliveryModal invoice={invoice} today={today} onClose={() => setModal(null)} />}
    </Card>
  );
};
