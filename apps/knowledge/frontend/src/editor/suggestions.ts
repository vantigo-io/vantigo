import { Extension } from "@tiptap/core";
import { PluginKey } from "@tiptap/pm/state";
import Suggestion from "@tiptap/suggestion";
import { SuggestionStore } from "./suggestion-store";

declare module "@tiptap/core" {
  interface Storage {
    slashMenu: { store: SuggestionStore };
    articleLinkPicker: { store: SuggestionStore };
  }
}

export const slashMenuKey = new PluginKey("slashMenu");
export const articleLinkPickerKey = new PluginKey("articleLinkPicker");

/**
 * The slash menu (knowledge design K18): `/` at the start of a line opens the
 * block menu. Not inside a code block, where a slash is code. The menu itself
 * is `SlashMenu`, which reads the store.
 */
export const SlashMenuExtension = Extension.create({
  name: "slashMenu",
  addStorage: () => ({ store: new SuggestionStore() }),
  addProseMirrorPlugins() {
    const { store } = this.storage;
    return [
      Suggestion({
        editor: this.editor,
        pluginKey: slashMenuKey,
        char: "/",
        startOfLine: true,
        allow: ({ state, range }) => state.doc.resolve(range.from).parent.type.name !== "codeBlock",
        render: store.render,
      }),
    ];
  },
});

/**
 * The article picker (K21): `[[` opens a search of the pages the person may
 * read; choosing one inserts an `articleLink`. The menu is `ArticleLinkPicker`.
 */
export const ArticleLinkPickerExtension = Extension.create({
  name: "articleLinkPicker",
  addStorage: () => ({ store: new SuggestionStore() }),
  addProseMirrorPlugins() {
    const { store } = this.storage;
    return [
      Suggestion({
        editor: this.editor,
        pluginKey: articleLinkPickerKey,
        char: "[[",
        allowSpaces: true,
        allowedPrefixes: null,
        allow: ({ state, range }) => state.doc.resolve(range.from).parent.type.name !== "codeBlock",
        render: store.render,
      }),
    ];
  },
});
