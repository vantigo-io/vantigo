import type { Node as PMNode } from "@tiptap/pm/model";
import type { Editor } from "@tiptap/react";

/** A heading as the table of contents lists it: its block id (the anchor), level, text and position. */
export interface TocHeading {
  id: string | null;
  level: number;
  text: string;
  pos: number;
}

/** The document's headings, in order, with the text they show. Empty headings are left out. */
export const documentHeadings = (doc: PMNode): TocHeading[] => {
  const headings: TocHeading[] = [];
  doc.descendants((node, pos) => {
    if (node.type.name !== "heading") return true;
    const text = node.textContent.trim();
    if (text)
      headings.push({ id: (node.attrs.id as string | null) ?? null, level: Number(node.attrs.level) || 1, text, pos });
    return false;
  });
  return headings;
};

/** Scrolls the heading into view and, while editing, puts the cursor at its start. */
export const goToHeading = (editor: Editor, heading: TocHeading) => {
  const dom = editor.view.nodeDOM(heading.pos);
  if (dom instanceof HTMLElement) dom.scrollIntoView({ behavior: "smooth", block: "start" });
  if (editor.isEditable) editor.commands.setTextSelection(heading.pos + 1);
};
