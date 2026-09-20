import { ActionIcon, Anchor, Group, Image, Stack, Text, UnstyledButton } from "@mantine/core";
import { IconFileText, IconX } from "@tabler/icons-react";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { attachmentUrl } from "../api/attachments";
import type { ExpenseAttachment } from "../api/entries";
import "../i18n";
import { formatFileSize, isInlineImage } from "../lib/receipts";
import { ReceiptViewer } from "./receipt-viewer";

export interface ReceiptThumbnailsProps {
  attachments: ExpenseAttachment[];
  /** Larger squares in the form; the list row keeps them small. */
  size?: number;
  /** Offered only while the expense is still its owner's to change. */
  onRemove?: (id: number) => void;
}

/**
 * An expense's receipts beside it. A JPEG or a PNG is drawn inline — an image
 * subresource is not a document, so the platform's `frame-ancestors 'none'`
 * does not touch it — and opens in the viewer. A HEIC or a PDF gets a link
 * that opens in a tab of its own, with the file's name and size, because
 * neither can be drawn in the page and neither may be framed.
 */
export const ReceiptThumbnails = ({ attachments, size = 48, onRemove }: ReceiptThumbnailsProps) => {
  const { t, formatters } = useI18n("expenses");
  const images = attachments.filter((one) => isInlineImage(one.contentType));
  const [viewing, setViewing] = useState<number | null>(null);

  if (attachments.length === 0) return null;

  return (
    <Stack gap={4}>
      <Group gap="xs" wrap="wrap">
        {attachments.map((one) => {
          const remove = onRemove && (
            <ActionIcon
              size="sm"
              variant="subtle"
              color="red"
              aria-label={t("removeReceipt", { name: one.fileName })}
              onClick={() => onRemove(one.id)}
            >
              <IconX size={14} />
            </ActionIcon>
          );
          if (isInlineImage(one.contentType)) {
            return (
              <Group key={one.id} gap={2} wrap="nowrap">
                <UnstyledButton
                  aria-label={t("viewReceipt", { name: one.fileName })}
                  onClick={() => setViewing(images.indexOf(one))}
                >
                  <Image src={attachmentUrl(one.id)} alt={one.fileName} w={size} h={size} fit="cover" radius="sm" />
                </UnstyledButton>
                {remove}
              </Group>
            );
          }
          return (
            <Group key={one.id} gap={2} wrap="nowrap">
              <Anchor
                href={attachmentUrl(one.id)}
                target="_blank"
                rel="noopener noreferrer"
                aria-label={t("openReceipt", { name: one.fileName })}
                size="sm"
              >
                <Group gap={4} wrap="nowrap">
                  <IconFileText size={16} />
                  <Text span size="sm">
                    {one.fileName}
                  </Text>
                  <Text span size="xs" c="dimmed">
                    {formatFileSize(one.sizeBytes, (value) => formatters.formatNumber(value))}
                  </Text>
                </Group>
              </Anchor>
              {remove}
            </Group>
          );
        })}
      </Group>
      <ReceiptViewer attachments={images} index={viewing} onIndexChange={setViewing} onClose={() => setViewing(null)} />
    </Stack>
  );
};
