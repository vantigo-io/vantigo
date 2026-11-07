import type { Editor, Range } from "@tiptap/react";
import type { SuggestionOptions } from "@tiptap/suggestion";

/** What an open suggestion (`/`, `@`, `[[`) tells its menu: the typed query, where it is and the range it replaces. */
export interface SuggestionState {
  active: boolean;
  query: string;
  range: Range | null;
  rect: DOMRect | null;
  editor: Editor | null;
}

const closed: SuggestionState = { active: false, query: "", range: null, rect: null, editor: null };

/**
 * The bridge between a TipTap suggestion plugin and the React menu that shows
 * it (knowledge design K18, K20, K21). The plugin only says what was typed and
 * where; the menu, a Mantine popover, owns the items, the highlighted one and
 * the choice, so the items can come from the catalog (in the reader's
 * language) or from a query. The plugin hands every key press to `onKey`
 * first, which is how the arrows, Enter and Escape reach the menu.
 */
export class SuggestionStore {
  private state: SuggestionState = closed;
  private readonly listeners = new Set<() => void>();
  private onKey: ((event: KeyboardEvent) => boolean) | null = null;

  readonly get = () => this.state;

  readonly subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };

  private set(next: SuggestionState) {
    this.state = next;
    for (const listener of this.listeners) listener();
  }

  /**
   * The menu's key handler while it is mounted (true when it used the key),
   * and what takes it away again.
   */
  readonly setKeyHandler = (handler: (event: KeyboardEvent) => boolean) => {
    this.onKey = handler;
    return () => {
      if (this.onKey === handler) this.onKey = null;
    };
  };

  /** Closes the menu by hand (Escape, a click outside), as the plugin does when the query is gone. */
  readonly close = () => this.set(closed);

  /** The `render` of a suggestion plugin's options, feeding this store. */
  readonly render = (): ReturnType<NonNullable<SuggestionOptions["render"]>> => {
    const update = (props: {
      editor: Editor;
      query: string;
      range: Range;
      clientRect?: (() => DOMRect | null) | null;
    }) =>
      this.set({
        active: true,
        query: props.query,
        range: props.range,
        rect: props.clientRect?.() ?? null,
        editor: props.editor,
      });
    return {
      onStart: update,
      onUpdate: update,
      onExit: () => this.close(),
      onKeyDown: ({ event }) => {
        if (!this.state.active) return false;
        if (event.key === "Escape") {
          this.close();
          return true;
        }
        return this.onKey?.(event) ?? false;
      },
    };
  };
}
