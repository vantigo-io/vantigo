import { Badge, Button, Group, Text } from "@mantine/core";
import { IconAlertTriangle, IconPaperclip } from "@tabler/icons-react";
import { useI18n } from "@vantigo/frontend-shell";
import type { Attachment } from "../api/conversations";
import { attachmentDownloadUrl } from "../api/conversations";

/**
 * A message's files, each a download — scanned ones open, ones nothing scanned download with a warning,
 * and one not ready says so. The image of one of a message's cards (role "card") is the carousel's, never
 * listed here.
 */
export function AttachmentLinks({ attachments }: { attachments: Attachment[] }) {
  const { t } = useI18n("communications");
  const files = attachments.filter((file) => file.role !== "card");
  const unscanned = files.some((file) => file.downloadAvailable && file.scanStatus === "unscanned");
  if (files.length === 0) return null;
  return (
    <>
      <Group mt="sm" gap="xs">
        {files.map((file) =>
          file.downloadAvailable && file.scanStatus === "clean" ? (
            <Button
              key={file.id}
              component="a"
              href={attachmentDownloadUrl(file.downloadPath)}
              target="_blank"
              rel="noreferrer"
              size="compact-xs"
              variant="subtle"
              leftSection={<IconPaperclip size={12} />}
            >
              {file.fileName}
            </Button>
          ) : file.downloadAvailable && file.scanStatus === "unscanned" ? (
            <Button
              key={file.id}
              component="a"
              href={attachmentDownloadUrl(file.downloadPath)}
              download={file.fileName}
              size="compact-xs"
              variant="subtle"
              color="orange"
              title={t("attachmentNotScanned")}
              leftSection={<IconAlertTriangle size={12} />}
            >
              {file.fileName}
            </Button>
          ) : (
            <Badge key={file.id} leftSection={<IconPaperclip size={12} />} variant="outline">
              {file.scanStatus === "pending" ? t("scanning") : t("attachmentUnavailable")}
            </Badge>
          ),
        )}
      </Group>
      {unscanned && (
        <Text size="xs" c="orange" mt={4}>
          {t("attachmentNotScanned")}
        </Text>
      )}
    </>
  );
}
