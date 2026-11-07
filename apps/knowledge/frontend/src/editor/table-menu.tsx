import { ActionIcon, Group, Paper, Tooltip } from "@mantine/core";
import type { Editor } from "@tiptap/core";
import { PluginKey } from "@tiptap/pm/state";
import { BubbleMenu } from "@tiptap/react/menus";
import { useI18n } from "@vantigo/frontend-shell";
import { tableCommands } from "./table-commands";

const tableMenuKey = new PluginKey("tableMenu");

/**
 * The cell menu (K18): while the caret is in a table, a bar under it adds or
 * removes rows and columns, turns the header row on or off, or removes the
 * table.
 */
export const TableMenu = ({ editor }: { editor: Editor }) => {
  const { t } = useI18n("knowledge");
  return (
    <BubbleMenu
      editor={editor}
      pluginKey={tableMenuKey}
      shouldShow={({ editor: current }) => current.isEditable && current.isActive("table")}
      options={{ placement: "bottom" }}
    >
      <Paper shadow="sm" withBorder p={2}>
        <Group gap={2} role="toolbar" aria-label={t("tableMenuLabel")}>
          {tableCommands.map((command) => (
            <Tooltip key={command.key} label={t(command.label)} withinPortal>
              <ActionIcon
                variant="subtle"
                color="gray"
                size="md"
                aria-label={t(command.label)}
                onMouseDown={(event) => event.preventDefault()}
                onClick={() => command.run(editor.chain().focus()).run()}
              >
                <command.icon size={18} aria-hidden />
              </ActionIcon>
            </Tooltip>
          ))}
        </Group>
      </Paper>
    </BubbleMenu>
  );
};
