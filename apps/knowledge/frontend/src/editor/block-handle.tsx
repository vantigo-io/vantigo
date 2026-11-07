import { ActionIcon, Group } from "@mantine/core";
import { IconGripVertical, IconPlus } from "@tabler/icons-react";
import type { Editor } from "@tiptap/core";
import DragHandle from "@tiptap/extension-drag-handle-react";
import type { Node as PMNode } from "@tiptap/pm/model";
import { useI18n } from "@vantigo/frontend-shell";
import { useRef } from "react";
import { openSlashMenuAt } from "./block-commands";

/**
 * The handle beside the block under the pointer (K18), while editing: `+`
 * opens the slash menu there, and the grip drags the block to another place.
 */
export const BlockHandle = ({ editor }: { editor: Editor }) => {
  const { t } = useI18n("knowledge");
  const current = useRef<{ node: PMNode; pos: number } | null>(null);
  return (
    <DragHandle
      editor={editor}
      onNodeChange={({ node, pos }) => {
        current.current = node ? { node, pos } : null;
      }}
    >
      <Group gap={0} wrap="nowrap" style={{ marginRight: 4 }}>
        <ActionIcon
          variant="subtle"
          color="gray"
          size="sm"
          aria-label={t("blockAdd")}
          onClick={() => {
            if (current.current) openSlashMenuAt(editor, current.current.node, current.current.pos);
          }}
        >
          <IconPlus size={16} aria-hidden />
        </ActionIcon>
        <ActionIcon
          component="div"
          variant="subtle"
          color="gray"
          size="sm"
          aria-label={t("blockDrag")}
          style={{ cursor: "grab" }}
        >
          <IconGripVertical size={16} aria-hidden />
        </ActionIcon>
      </Group>
    </DragHandle>
  );
};
