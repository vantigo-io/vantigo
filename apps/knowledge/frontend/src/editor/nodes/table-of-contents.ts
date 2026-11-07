import { mergeAttributes, Node } from "@tiptap/core";
import { ReactNodeViewRenderer } from "@tiptap/react";
import { TableOfContentsView } from "./table-of-contents-view";

/**
 * The table of contents (knowledge design K18): a block without attributes
 * or content that lists the document's headings as links to them, kept
 * current as the headings change.
 */
export const TableOfContents = Node.create({
  name: "tableOfContents",
  group: "block",
  atom: true,
  selectable: true,
  draggable: true,

  parseHTML() {
    return [{ tag: 'nav[data-type="tableOfContents"]' }];
  },

  renderHTML({ HTMLAttributes }) {
    return ["nav", mergeAttributes(HTMLAttributes, { "data-type": "tableOfContents" })];
  },

  addNodeView() {
    return ReactNodeViewRenderer(TableOfContentsView);
  },
});
