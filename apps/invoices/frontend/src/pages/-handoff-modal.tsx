import { Alert, Button, Checkbox, Group, Modal, Stack, Text, Textarea, TextInput } from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { notifications } from "@mantine/notifications";
import { IconAlertTriangle } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { handOff, type InvoiceHandoff, type LetterLeft, withdrawHandoff } from "../api/collection";
import type { InvoiceDocument } from "../api/invoices";
import { ApiValidationError, INVOICES_QUERY_KEY } from "../api/request";
import "../i18n";
import { fieldRefusals, refusalCode, refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";

const handoffInputs = new Set(["handedOn", "agency", "agencyReference", "note"]);

export interface HandoffModalProps {
  invoice: InvoiceDocument;
  /** Today in Oslo, meta's: the day prefilled and the latest a hand-off may be. */
  today: string;
  /** Whether the caller may record a delivery (`invoices:issue`), the way out of `invoice_not_delivered`. */
  canIssue: boolean;
  /** Closes this dialog and opens "Record a delivery". */
  onRecordDelivery: () => void;
  onLettersLeft: (letters: LetterLeft[]) => void;
  onClose: () => void;
}

/**
 * Records the hand-off to a collection agency (invoices payments and
 * reminders design D11), made outside Vantigo: the day, the agency and its
 * case number, with a note. An invoice with no delivery by its due date is
 * refused `invoice_not_delivered`, in words: if it was delivered, the person
 * records the delivery first; otherwise ticks that the hand-off is made
 * anyway and sends it again.
 */
export const HandoffModal = ({
  invoice,
  today,
  canIssue,
  onRecordDelivery,
  onLettersLeft,
  onClose,
}: HandoffModalProps) => {
  const { t, date } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const [handedOn, setHandedOn] = useState<string | null>(today);
  const [agency, setAgency] = useState("");
  const [agencyReference, setAgencyReference] = useState("");
  const [note, setNote] = useState("");
  const [notDelivered, setNotDelivered] = useState(false);
  const [acknowledged, setAcknowledged] = useState(false);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const clear = (field: string) =>
    setErrors((current) => Object.fromEntries(Object.entries(current).filter(([f]) => f !== field)));
  const save = useMutation({
    mutationFn: () =>
      handOff(invoice.id, {
        handedOn: handedOn ?? "",
        agency: agency.trim(),
        ...(agencyReference.trim() ? { agencyReference: agencyReference.trim() } : {}),
        ...(note.trim() ? { note: note.trim() } : {}),
        ...(acknowledged ? { acknowledgeNotDelivered: true } : {}),
      }),
    onSuccess: async (result) => {
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      notifications.show({ color: "green", message: t("handoff.recorded") });
      onLettersLeft(result.lettersLeft);
      onClose();
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        const { onInputs, elsewhere } = fieldRefusals(error, t, (field) => handoffInputs.has(field), "handoff");
        setErrors(onInputs);
        if (elsewhere.length > 0) {
          notifications.show({ color: "red", title: t("handoff.couldNotRecord"), message: elsewhere.join(" ") });
        }
        return;
      }
      if (refusalCode(error) === "invoice_not_delivered") {
        setNotDelivered(true);
        return;
      }
      // The payments' own words for invoice_settled speak of registering a
      // payment; a hand-off is refused because no claim is left to hand off.
      if (refusalCode(error) === "invoice_settled") {
        notifications.show({ color: "red", title: t("handoff.couldNotRecord"), message: t("handoff.settled") });
        return;
      }
      notifications.show({ color: "red", title: t("handoff.couldNotRecord"), message: refusalMessage(error, t, date) });
    },
  });
  return (
    <Modal opened onClose={onClose} title={t("handoff.handOff")}>
      <Stack>
        <Text size="sm">{t("handoff.handOffHint")}</Text>
        <DateInput
          label={t("handoff.handedOn")}
          required
          valueFormat={t("dateInputFormat")}
          minDate={invoice.issueDate}
          maxDate={today}
          value={handedOn}
          error={errors.handedOn}
          onChange={(d) => {
            setHandedOn(d);
            clear("handedOn");
          }}
        />
        <TextInput
          label={t("handoff.agency")}
          required
          maxLength={200}
          value={agency}
          error={errors.agency}
          onChange={(e) => {
            setAgency(e.currentTarget.value);
            clear("agency");
          }}
        />
        <TextInput
          label={t("handoff.agencyReference")}
          maxLength={100}
          value={agencyReference}
          error={errors.agencyReference}
          onChange={(e) => {
            setAgencyReference(e.currentTarget.value);
            clear("agencyReference");
          }}
        />
        <Textarea
          label={t("note")}
          maxLength={500}
          value={note}
          error={errors.note}
          onChange={(e) => {
            setNote(e.currentTarget.value);
            clear("note");
          }}
        />
        {notDelivered && (
          <Alert color="yellow" icon={<IconAlertTriangle size={16} />} data-testid="not-delivered">
            <Stack gap="xs">
              <Text size="sm">{t("refusal.invoice_not_delivered")}</Text>
              {canIssue && (
                <Group>
                  <Button size="xs" variant="default" onClick={onRecordDelivery}>
                    {t("handoff.wasDelivered")}
                  </Button>
                </Group>
              )}
              <Checkbox
                label={t("handoff.acknowledgeNotDelivered")}
                checked={acknowledged}
                onChange={(e) => setAcknowledged(e.currentTarget.checked)}
              />
            </Stack>
          </Alert>
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button
            disabled={save.isPending || !handedOn || agency.trim() === "" || (notDelivered && !acknowledged)}
            loading={save.isPending}
            onClick={() => save.mutate()}
          >
            {t("handoff.handOff")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};

export interface WithdrawHandoffModalProps {
  invoice: InvoiceDocument;
  handoff: InvoiceHandoff;
  today: string;
  onLettersLeft: (letters: LetterLeft[]) => void;
  onClose: () => void;
}

/**
 * Records that the claim came back from the agency (D11): the day, from the
 * hand-off's own day to today, and why. Letters may follow again.
 */
export const WithdrawHandoffModal = ({
  invoice,
  handoff,
  today,
  onLettersLeft,
  onClose,
}: WithdrawHandoffModalProps) => {
  const { t, date } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const [withdrawnOn, setWithdrawnOn] = useState<string | null>(today);
  const [reason, setReason] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const save = useMutation({
    mutationFn: () => withdrawHandoff(invoice.id, { withdrawnOn: withdrawnOn ?? "", reason: reason.trim() }),
    onSuccess: async (result) => {
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      notifications.show({ color: "green", message: t("handoff.withdrawn") });
      onLettersLeft(result.lettersLeft);
      onClose();
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        const { onInputs, elsewhere } = fieldRefusals(
          error,
          t,
          (field) => field === "withdrawnOn" || field === "reason",
          "handoffWithdraw",
        );
        setErrors(onInputs);
        if (elsewhere.length > 0) {
          notifications.show({ color: "red", title: t("handoff.couldNotWithdraw"), message: elsewhere.join(" ") });
        }
        return;
      }
      notifications.show({
        color: "red",
        title: t("handoff.couldNotWithdraw"),
        message: refusalMessage(error, t, date),
      });
    },
  });
  return (
    <Modal opened onClose={onClose} title={t("handoff.withdraw")}>
      <Stack>
        <Text size="sm">{t("handoff.withdrawHint", { agency: handoff.agency })}</Text>
        <DateInput
          label={t("handoff.withdrawnOn")}
          required
          valueFormat={t("dateInputFormat")}
          minDate={handoff.handedOn}
          maxDate={today}
          value={withdrawnOn}
          error={errors.withdrawnOn}
          onChange={(d) => {
            setWithdrawnOn(d);
            setErrors((current) => Object.fromEntries(Object.entries(current).filter(([f]) => f !== "withdrawnOn")));
          }}
        />
        <Textarea
          label={t("removalReason")}
          required
          maxLength={200}
          value={reason}
          error={errors.reason}
          onChange={(e) => {
            setReason(e.currentTarget.value);
            setErrors((current) => Object.fromEntries(Object.entries(current).filter(([f]) => f !== "reason")));
          }}
        />
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button
            disabled={save.isPending || !withdrawnOn || reason.trim() === ""}
            loading={save.isPending}
            onClick={() => save.mutate()}
          >
            {t("handoff.withdraw")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
