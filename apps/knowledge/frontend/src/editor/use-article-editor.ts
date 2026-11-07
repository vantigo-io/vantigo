import { type Editor, useEditor } from "@tiptap/react";
import { useI18n } from "@vantigo/frontend-shell";
import { useEffect } from "react";
import type { KnowledgeDocument } from "../api/articles";
import { editorExtensions } from "./extensions";

/**
 * The article's editor (knowledge design K7) as a hook: TipTap with the
 * package's extensions, editable or not. The page owns it, so it can read and
 * replace the document (autosave, a reload, a restored draft).
 */
export const useArticleEditor = ({
  content,
  editable,
  onUpdate,
}: {
  content: KnowledgeDocument;
  editable: boolean;
  onUpdate?: (editor: Editor) => void;
}) => {
  const { t } = useI18n("knowledge");
  const editor = useEditor({
    extensions: editorExtensions(t("editorPlaceholder")),
    content,
    editable,
    immediatelyRender: true,
    shouldRerenderOnTransaction: false,
    // A replaced document (setContent with emitUpdate false) does not call this: only typing does.
    onUpdate: ({ editor: current }) => onUpdate?.(current),
  });
  useEffect(() => {
    if (editor && editor.isEditable !== editable) editor.setEditable(editable, false);
  }, [editor, editable]);
  return editor;
};
