import type { Attribute } from "@tiptap/core";
import Mention from "@tiptap/extension-mention";
import { PluginKey } from "@tiptap/pm/state";
import { ReactNodeViewRenderer } from "@tiptap/react";
import Suggestion from "@tiptap/suggestion";
import { SuggestionStore } from "../suggestion-store";
import { MentionView } from "./mention-view";

declare module "@tiptap/core" {
  interface Storage {
    mention: { store: SuggestionStore };
  }
}

export const mentionPickerKey = new PluginKey("mentionPicker");

/**
 * A mention of a person (knowledge design K20): `@` opens a search of the
 * user directory; the node holds the user's id and the name it had, and shows
 * the current one. TipTap's Mention, with only `id` and `label` (its
 * `mentionSuggestionChar` is always `@` here, so it is not stored), and the
 * picker as the other menus are: `MentionPicker` reads the store.
 */
export const KnowledgeMention = Mention.extend({
  addAttributes() {
    const { id, label } = (this.parent?.() ?? {}) as Record<string, Attribute>;
    return { id, label };
  },
  addStorage() {
    return { store: new SuggestionStore() };
  },
  addNodeView() {
    return ReactNodeViewRenderer(MentionView);
  },
  addProseMirrorPlugins() {
    return [
      Suggestion({
        editor: this.editor,
        pluginKey: mentionPickerKey,
        char: "@",
        allow: ({ state, range }) => state.doc.resolve(range.from).parent.type.name !== "codeBlock",
        render: this.storage.store.render,
      }),
    ];
  },
}).configure({
  renderText: ({ node }) => `@${node.attrs.label ?? ""}`,
});
