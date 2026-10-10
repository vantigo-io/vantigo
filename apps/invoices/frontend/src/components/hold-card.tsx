import { Badge, Button, Card, Group, Stack, Text, Title } from "@mantine/core";
import { useState } from "react";
import type { LetterLeft } from "../api/collection";
import type { InvoiceDocument } from "../api/invoices";
import "../i18n";
import { useWho } from "../lib/bank";
import { useInvoiceFormat } from "../lib/format";
import { HoldModal } from "../pages/-hold-modal";
import { LiftModal } from "../pages/-lift-modal";

export interface HoldCardProps {
  invoice: InvoiceDocument;
  /** meta's `canRegisterPayments` — `invoices:payments`, which a hold and its lift need. */
  canAct: boolean;
  currentUserId?: string;
  /** The letters a hold or its lift did not withdraw, for the page to name. */
  onLettersLeft: (letters: LetterLeft[]) => void;
}

/**
 * Whether the customer disputes the invoice (invoices payments and reminders
 * design D11, D22): a live hold — what is disputed, since when, by whom — with
 * "Lift the hold", or the last hold lifted and what its lift decided: fees and
 * the compensation barred on the invoice for good, or the objection found
 * groundless and the charges kept. "Put on hold" otherwise. Both actions need
 * `invoices:payments`.
 */
export const HoldCard = ({ invoice, canAct, currentUserId, onLettersLeft }: HoldCardProps) => {
  const { t, dateTime } = useInvoiceFormat();
  const who = useWho(currentUserId);
  const [modal, setModal] = useState<"hold" | "lift" | null>(null);
  const hold = invoice.hold;
  const live = hold !== undefined && !hold.liftedAt;
  return (
    <Card withBorder data-testid="hold-card">
      <Stack gap="xs">
        <Group justify="space-between">
          <Title order={4}>{t("hold.title")}</Title>
          {canAct &&
            (live ? (
              <Button size="xs" variant="default" onClick={() => setModal("lift")}>
                {t("hold.lift")}
              </Button>
            ) : (
              <Button size="xs" variant="default" onClick={() => setModal("hold")}>
                {t("hold.place")}
              </Button>
            ))}
        </Group>
        {live && hold && (
          <>
            <Group gap="xs">
              <Badge color="orange" variant="light">
                {t("hold.onHold")}
              </Badge>
              <Text size="sm">{t("hold.placedBy", { at: dateTime(hold.placedAt), who: who(hold.placedBy) })}</Text>
            </Group>
            <Text size="sm">{t("hold.disputes", { note: hold.note })}</Text>
            <Text size="xs" c="dimmed">
              {t("hold.whileHeld")}
            </Text>
          </>
        )}
        {!live && hold?.liftedAt && (
          <>
            <Text size="sm">
              {t("hold.liftedBy", { at: dateTime(hold.liftedAt), who: who(hold.liftedBy), note: hold.note })}
            </Text>
            {hold.liftNote && <Text size="sm">{t("hold.liftNoteIs", { note: hold.liftNote })}</Text>}
            <Text size="sm" fw={600} data-testid="hold-outcome">
              {hold.chargesAllowed === false ? t("hold.chargesBarred") : t("hold.chargesKept")}
            </Text>
          </>
        )}
        {!hold && (
          <Text size="sm" c="dimmed">
            {t("hold.none")}
          </Text>
        )}
      </Stack>
      {modal === "hold" && <HoldModal invoice={invoice} onLettersLeft={onLettersLeft} onClose={() => setModal(null)} />}
      {modal === "lift" && <LiftModal invoice={invoice} onLettersLeft={onLettersLeft} onClose={() => setModal(null)} />}
    </Card>
  );
};
