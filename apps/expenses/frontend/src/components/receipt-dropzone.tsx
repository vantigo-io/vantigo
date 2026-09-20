import { Box, Group, Loader, Stack, Text } from "@mantine/core";
import { useI18n } from "@vantigo/frontend-shell";
import { type ChangeEvent, type DragEvent, useState } from "react";
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
export const ReceiptDropzone = ({ entryId, attachmentCount, onUploaded }: ReceiptDropzoneProps) => {
  const { t } = useI18n("expenses");
  const [pending, setPending] = useState<Pending[]>([]);
  const [busy, setBusy] = useState(false);
  const [over, setOver] = useState(false);

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
        failed.push({ name: file.name, error: messageFor(error as Error) });
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

  const drop = (event: DragEvent<HTMLDivElement>) => {
    event.preventDefault();
    setOver(false);
    const files = Array.from(event.dataTransfer?.files ?? []);
    if (files.length > 0) void send(files);
  };

  return (
    <Stack gap="xs">
      <Box
        p="sm"
        style={{
          border: "1px dashed var(--mantine-color-gray-4)",
          borderRadius: "var(--mantine-radius-sm)",
          background: over ? "var(--mantine-color-gray-0)" : undefined,
        }}
        onDragOver={(event) => {
          event.preventDefault();
          setOver(true);
        }}
        onDragLeave={() => setOver(false)}
        onDrop={drop}
      >
        <Stack gap="xs">
          <Text size="sm" c="dimmed">
            {t("dropReceiptsHere")}
          </Text>
          <Group gap="sm" wrap="wrap">
            <input type="file" multiple accept={RECEIPT_ACCEPT} aria-label={t("addReceipts")} onChange={pick} />
            <input type="file" accept="image/*" capture="environment" aria-label={t("takePhoto")} onChange={pick} />
          </Group>
        </Stack>
      </Box>
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
        .map((one) => (
          <Text key={one.name} size="sm" c="red">
            {one.error}
          </Text>
        ))}
    </Stack>
  );
};
