import type { Editor } from "@tiptap/core";
import type { Node as PMNode } from "@tiptap/pm/model";

/**
 * Opens the slash menu for the block at `pos` (knowledge design K18): in the
 * block itself when it is an empty paragraph, else in a new paragraph under it.
 */
export const openSlashMenuAt = (editor: Editor, node: PMNode, pos: number) => {
  if (node.type.name === "paragraph" && node.content.size === 0) {
    editor
      .chain()
      .focus()
      .setTextSelection(pos + 1)
      .insertContent("/")
      .run();
    return;
  }
  const after = pos + node.nodeSize;
  editor
    .chain()
    .focus()
    .insertContentAt(after, { type: "paragraph" })
    .setTextSelection(after + 1)
    .insertContent("/")
    .run();
};
