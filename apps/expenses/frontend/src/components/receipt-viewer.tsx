import { ActionIcon, Group, Image, Modal, Text } from "@mantine/core";
import { IconChevronLeft, IconChevronRight } from "@tabler/icons-react";
import { useI18n } from "@vantigo/frontend-shell";
import { useEffect } from "react";
import { attachmentUrl } from "../api/attachments";
import type { ExpenseAttachment } from "../api/entries";
import "../i18n";

export interface ReceiptViewerProps {
  /** The receipts of one expense, in the order they are shown beside it. */
  attachments: ExpenseAttachment[];
  /** Which one is open, or null when the viewer is closed. */
  index: number | null;
  onIndexChange: (index: number) => void;
  onClose: () => void;
}

/**
 * One receipt, large. Only JPEG and PNG reach it — HEIC and PDF open in a tab
 * of their own instead, because framing a receipt is impossible on purpose
 * (the platform sends `X-Frame-Options: DENY` and `object-src 'none'`, and
 * the receipt carries its own `default-src 'none'; sandbox`).
 *
 * It is operable from the keyboard: Escape closes it, which is Mantine's own
 * behaviour, and the arrow keys walk the expense's receipts. The alt text is
 * the file name, which is the only name a receipt has.
 */
export const ReceiptViewer = ({ attachments, index, onIndexChange, onClose }: ReceiptViewerProps) => {
  const { t } = useI18n("expenses");
  const at = index !== null && index >= 0 && index < attachments.length ? index : undefined;
  const current = at === undefined ? undefined : attachments[at];

  useEffect(() => {
    if (at === undefined) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "ArrowRight" && at + 1 < attachments.length) onIndexChange(at + 1);
      if (event.key === "ArrowLeft" && at - 1 >= 0) onIndexChange(at - 1);
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [at, attachments.length, onIndexChange]);

  return (
    <Modal opened={at !== undefined} onClose={onClose} title={current?.fileName ?? ""} size="xl" centered>
      {current !== undefined && at !== undefined && (
        <>
          <Image src={attachmentUrl(current.id)} alt={current.fileName} fit="contain" mah={600} />
          {attachments.length > 1 && (
            <Group justify="center" gap="sm" mt="sm">
              <ActionIcon
                variant="default"
                aria-label={t("previousReceipt")}
                disabled={at === 0}
                onClick={() => onIndexChange(at - 1)}
              >
                <IconChevronLeft size={16} />
              </ActionIcon>
              <Text size="sm" c="dimmed">
                {t("receiptPosition", { index: at + 1, total: attachments.length })}
              </Text>
              <ActionIcon
                variant="default"
                aria-label={t("nextReceipt")}
                disabled={at === attachments.length - 1}
                onClick={() => onIndexChange(at + 1)}
              >
                <IconChevronRight size={16} />
              </ActionIcon>
            </Group>
          )}
        </>
      )}
    </Modal>
  );
};
