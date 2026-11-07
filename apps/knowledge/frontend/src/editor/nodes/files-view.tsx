import { Anchor, Group, SegmentedControl, Text } from "@mantine/core";
import { IconDownload, IconPaperclip } from "@tabler/icons-react";
import type { ReactNodeViewProps } from "@tiptap/react";
import { NodeViewWrapper } from "@tiptap/react";
import { appUrl, useI18n } from "@vantigo/frontend-shell";
import { fileIdOfSrc, filePath } from "../../api/files";
import { formatBytes } from "../../lib/bytes";

/** The image widths the editor offers: small, medium, or the page's width (null). */
const widths = { small: 240, medium: 480, full: null } as const;
type Width = keyof typeof widths;

const widthOf = (value: unknown): Width =>
  value === widths.small ? "small" : value === widths.medium ? "medium" : "full";

export const ImageView = ({ node, updateAttributes, selected, editor }: ReactNodeViewProps) => {
  const { t } = useI18n("knowledge");
  const src = typeof node.attrs.src === "string" && fileIdOfSrc(node.attrs.src) ? node.attrs.src : null;
  const width = typeof node.attrs.width === "number" ? node.attrs.width : undefined;
  return (
    <NodeViewWrapper data-type="image" style={{ margin: "var(--mantine-spacing-sm) 0" }}>
      {src ? (
        <img
          src={appUrl(src)}
          alt={node.attrs.alt ?? ""}
          title={node.attrs.title ?? undefined}
          draggable={false}
          style={{
            display: "block",
            maxWidth: "100%",
            width: width ? `${width}px` : undefined,
            borderRadius: "var(--mantine-radius-sm)",
            outline: selected ? "2px solid var(--mantine-primary-color-filled)" : undefined,
          }}
        />
      ) : (
        <Text size="sm" c="dimmed">
          {t("imageMissing")}
        </Text>
      )}
      {editor.isEditable && selected && (
        <div contentEditable={false}>
          <SegmentedControl
            mt={4}
            size="xs"
            aria-label={t("imageWidth")}
            value={widthOf(width)}
            onChange={(value) => updateAttributes({ width: widths[value as Width] })}
            data={[
              { value: "small", label: t("imageSmall") },
              { value: "medium", label: t("imageMedium") },
              { value: "full", label: t("imageFull") },
            ]}
          />
        </div>
      )}
    </NodeViewWrapper>
  );
};

export const FileAttachmentView = ({ node, selected }: ReactNodeViewProps) => {
  const { t } = useI18n("knowledge");
  const fileId = typeof node.attrs.fileId === "string" ? node.attrs.fileId : null;
  const filename = (node.attrs.filename as string | null) || t("fileUnnamed");
  const size = formatBytes(node.attrs.size as number | null);
  return (
    <NodeViewWrapper data-type="fileAttachment" style={{ margin: "var(--mantine-spacing-xs) 0" }}>
      <Group
        gap="xs"
        wrap="nowrap"
        px="sm"
        py={6}
        contentEditable={false}
        style={{
          display: "inline-flex",
          border: `1px solid ${selected ? "var(--mantine-primary-color-filled)" : "var(--mantine-color-default-border)"}`,
          borderRadius: "var(--mantine-radius-md)",
          maxWidth: "100%",
        }}
      >
        <IconPaperclip size={16} aria-hidden />
        <Text size="sm" truncate>
          {filename}
        </Text>
        {size && (
          <Text size="xs" c="dimmed">
            {size}
          </Text>
        )}
        {fileId && (
          <Anchor
            href={appUrl(filePath(fileId))}
            download={filename}
            aria-label={t("fileDownload", { name: filename })}
          >
            <IconDownload size={16} aria-hidden />
          </Anchor>
        )}
      </Group>
    </NodeViewWrapper>
  );
};
