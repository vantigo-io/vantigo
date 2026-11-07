import { Group, Text } from "@mantine/core";
import type { Editor } from "@tiptap/core";
import { useI18n } from "@vantigo/frontend-shell";
import { useSyncExternalStore } from "react";
import { filterBlocks, type SlashBlock, slashBlocks } from "./blocks";
import { SuggestionMenu } from "./suggestion-menu";

/**
 * The slash menu's list (knowledge design K18): every block, filtered by what
 * follows `/` in either language. Choosing one removes the `/query` and
 * inserts the block there; an image or a file asks `onUpload` for the picker.
 * Blocks the editor cannot take where the caret is (a table in a table) are
 * left out.
 */
export const SlashMenu = ({ editor, onUpload }: { editor: Editor; onUpload?: (accept: "image" | "file") => void }) => {
  const { t } = useI18n("knowledge");
  const store = editor.storage.slashMenu.store;
  const { active, query } = useSyncExternalStore(store.subscribe, store.get);
  const available = active
    ? slashBlocks.filter((block) => {
        if (block.action.kind === "upload") return Boolean(onUpload);
        return block.action.run(editor.can().chain()).run();
      })
    : [];
  const items = filterBlocks(available, query, (block) => t(block.label));
  const choose = (block: SlashBlock, range: { from: number; to: number }, current: Editor) => {
    const start = current.chain().focus().deleteRange(range);
    if (block.action.kind === "chain") block.action.run(start).run();
    else {
      start.run();
      onUpload?.(block.action.accept);
    }
  };
  return (
    <SuggestionMenu
      store={store}
      items={items}
      itemKey={(block) => block.key}
      label={t("slashMenuLabel")}
      empty={t("slashMenuNothing")}
      onChoose={choose}
      renderItem={(block) => (
        <Group gap="sm" wrap="nowrap">
          <block.icon size={18} aria-hidden />
          <Text size="sm">{t(block.label)}</Text>
        </Group>
      )}
    />
  );
};
