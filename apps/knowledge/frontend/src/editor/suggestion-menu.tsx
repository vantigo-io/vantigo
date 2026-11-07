import { Group, Loader, Paper, Portal, ScrollArea, Text, UnstyledButton } from "@mantine/core";
import type { Editor, Range } from "@tiptap/core";
import { type ReactNode, useEffect, useId, useRef, useState, useSyncExternalStore } from "react";
import type { SuggestionStore } from "./suggestion-store";

/** Where the menu goes: under the typed text, kept inside the window. */
const placement = (rect: DOMRect | null) => {
  const top = (rect?.bottom ?? 0) + 4;
  const left = Math.max(8, Math.min(rect?.left ?? 0, window.innerWidth - 328));
  return { top, left };
};

/**
 * The menu of an open suggestion (`/`, `@`, `[[`), a Mantine paper under the
 * caret (knowledge design K18): the items the caller computed from the query,
 * one highlighted, Up and Down to move, Enter or Tab to choose, Escape to
 * close. The caret stays in the editor, so the list is a listbox the editor
 * points at, not a focus trap.
 */
export function SuggestionMenu<T>({
  store,
  items,
  itemKey,
  renderItem,
  onChoose,
  label,
  empty,
  loading = false,
}: {
  store: SuggestionStore;
  items: readonly T[];
  itemKey: (item: T) => string;
  renderItem: (item: T) => ReactNode;
  onChoose: (item: T, range: Range, editor: Editor) => void;
  label: string;
  empty: string;
  loading?: boolean;
}) {
  const state = useSyncExternalStore(store.subscribe, store.get);
  // The highlighted item belongs to one query and list: a new one starts at the top.
  const listKey = `${state.query}\u0000${items.length}`;
  const [selection, setSelection] = useState({ listKey, index: 0 });
  const highlighted = selection.listKey === listKey ? Math.min(selection.index, Math.max(items.length - 1, 0)) : 0;
  const highlight = (index: number) => setSelection({ listKey, index });
  const id = useId();
  const listRef = useRef<HTMLDivElement>(null);

  const choose = (item: T | undefined) => {
    if (!item || !state.range || !state.editor) return;
    store.close();
    onChoose(item, state.range, state.editor);
  };

  const onKey = (event: KeyboardEvent) => {
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      if (items.length === 0) return true;
      const step = event.key === "ArrowDown" ? 1 : -1;
      highlight((highlighted + step + items.length) % items.length);
      return true;
    }
    if (event.key === "Enter" || event.key === "Tab") {
      if (items.length === 0) return false;
      choose(items[highlighted]);
      return true;
    }
    return false;
  };

  useEffect(() => store.setKeyHandler(onKey));

  useEffect(() => {
    listRef.current?.querySelector<HTMLElement>('[aria-selected="true"]')?.scrollIntoView?.({ block: "nearest" });
  }, [highlighted]);

  if (!state.active) return null;
  const { top, left } = placement(state.rect);
  return (
    <Portal>
      <Paper
        shadow="md"
        withBorder
        p={4}
        w={320}
        style={{ position: "fixed", top, left, zIndex: 300 }}
        onMouseDown={(event) => event.preventDefault()}
      >
        <ScrollArea.Autosize mah={320} type="auto">
          <div ref={listRef} role="listbox" id={id} aria-label={label}>
            {items.map((item, index) => (
              <UnstyledButton
                key={itemKey(item)}
                role="option"
                aria-selected={index === highlighted}
                onMouseEnter={() => highlight(index)}
                onClick={() => choose(item)}
                w="100%"
                px="xs"
                py={6}
                style={{
                  borderRadius: "var(--mantine-radius-sm)",
                  background: index === highlighted ? "var(--mantine-color-default-hover)" : undefined,
                }}
              >
                {renderItem(item)}
              </UnstyledButton>
            ))}
            {items.length === 0 && (
              <Group gap="xs" px="xs" py={6}>
                {loading && <Loader size="xs" />}
                <Text size="sm" c="dimmed">
                  {empty}
                </Text>
              </Group>
            )}
          </div>
        </ScrollArea.Autosize>
      </Paper>
    </Portal>
  );
}
