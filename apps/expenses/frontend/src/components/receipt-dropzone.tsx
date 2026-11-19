import { Group, Loader, Stack, Text } from "@mantine/core";
import { DropZone, useI18n } from "@vantigo/frontend-shell";
import { type ChangeEvent, useState } from "react";
import { uploadReceipt } from "../api/attachments";
import type { ExpenseAttachment } from "../api/entries";
import { type ApiError, ApiValidationError } from "../api/request";
import "../i18n";
import { checkReceiptFile, RECEIPT_ACCEPT, type ReceiptRefusal } from "../lib/receipts";

export interface ReceiptDropzoneProps {
  /**
   * The expense the receipts go on. Absent until it has been saved: a receipt
   * needs something to hang on, so the zone says so instead of pretending.
   */
  entryId?: number;
  /** How many the expense already carries, so an eleventh is stopped here. */
  attachmentCount: number;
  /** Called with each receipt as it lands, so the form can show it at once. */
  onUploaded: (attachment: ExpenseAttachment) => void;
  /**
   * Whether the document is a supplier's invoice rather than receipts. The
   * mechanism is the same; the words say what is being attached.
   */
  supplierInvoice?: boolean;
}

const refusalKey: Record<ReceiptRefusal, string> = {
  tooLarge: "receiptTooLarge",
  wrongType: "receiptWrongType",
  tooMany: "receiptTooMany",
};

/** One file the person picked, while it is on its way or after it was refused. */
interface Pending {
  name: string;
  error?: string;
}

/**
 * Where receipts are added: dropped on, picked from a file dialog, or
 * photographed on a phone. Size and type are checked here so nobody waits for
 * an upload that will be refused — but the server sniffs the bytes and has
 * the last word, and whatever it says is shown against the file it is about.
 */
export const ReceiptDropzone = ({ entryId, attachmentCount, onUploaded, supplierInvoice }: ReceiptDropzoneProps) => {
  const { t } = useI18n("expenses");
  const [pending, setPending] = useState<Pending[]>([]);
  const [busy, setBusy] = useState(false);

  if (entryId === undefined) {
    return (
      <Text size="sm" c="dimmed">
        {t("saveDraftFirst")}
      </Text>
    );
  }

  const messageFor = (error: Error): string => {
    if (error instanceof ApiValidationError) {
      const fields = error.fieldErrors;
      const named = fields.file ?? fields.entryId;
      if (named) return named;
    }
    const status = (error as ApiError).status;
    if (status === 429) return t("receiptRateLimited");
    if (status === 503) return t("receiptStoreDown");
    return error.message;
  };

  const send = async (files: File[]) => {
    setBusy(true);
    let carried = attachmentCount;
    const failed: Pending[] = [];
    for (const file of files) {
      const refusal = checkReceiptFile(file, carried);
      if (refusal) {
        failed.push({ name: file.name, error: t(refusalKey[refusal], { name: file.name }) });
        continue;
      }
      setPending([...failed, { name: file.name }]);
      try {
        onUploaded(await uploadReceipt(entryId, file));
        carried += 1;
      } catch (error) {
        // The server's sentence does not name the file; the title does, which
        // is what matters when several were dropped at once.
        failed.push({
          name: file.name,
          error: `${t("couldNotUploadReceipt", { name: file.name })}: ${messageFor(error as Error)}`,
        });
      }
    }
    setPending(failed);
    setBusy(false);
  };

  const pick = (event: ChangeEvent<HTMLInputElement>) => {
    const files = Array.from(event.target.files ?? []);
    event.target.value = "";
    if (files.length > 0) void send(files);
  };

  const drop = (files: File[]) => {
    if (files.length > 0) void send(files);
  };

  return (
    <Stack gap="xs">
      <DropZone p="sm" disabled={busy} onDropFiles={drop}>
        <Stack gap="xs">
          <Text size="sm" c="dimmed">
            {supplierInvoice ? t("dropSupplierInvoiceHere") : t("dropReceiptsHere")}
          </Text>
          <Group gap="sm" wrap="wrap">
            <input
              type="file"
              multiple
              accept={RECEIPT_ACCEPT}
              aria-label={supplierInvoice ? t("attachSupplierInvoice") : t("addReceipts")}
              onChange={pick}
            />
            <input type="file" accept="image/*" capture="environment" aria-label={t("takePhoto")} onChange={pick} />
          </Group>
        </Stack>
      </DropZone>
      {busy && (
        <Group gap="xs">
          <Loader size="xs" />
          <Text size="sm" c="dimmed">
            {pending.at(-1)?.name}
          </Text>
        </Group>
      )}
      {pending
        .filter((one) => one.error)
        // Two folders can hand over the same file name, so the index is part
        // of the key: one refusal must not stand in for the other.
        .map((one, index) => (
          <Text key={`${one.name}:${index}`} size="sm" c="red">
            {one.error}
          </Text>
        ))}
    </Stack>
  );
};
