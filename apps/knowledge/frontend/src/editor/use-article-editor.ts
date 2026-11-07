import { type Editor, useEditor } from "@tiptap/react";
import { useI18n } from "@vantigo/frontend-shell";
import { useEffect } from "react";
import type { KnowledgeDocument } from "../api/articles";
import { recordModules } from "../lib/record-modules";
import { editorExtensions } from "./extensions";

/**
 * The article's editor (knowledge design K7) as a hook: TipTap with the
 * package's extensions, editable or not. The page owns it, so it can read and
 * replace the document (autosave, a reload, a restored draft).
 */
export const useArticleEditor = ({
  content,
  editable,
  articleId,
  onUpdate,
}: {
  content: KnowledgeDocument;
  /** The article the editor is for, which its uploads go to (K19). */
  articleId?: string;
  editable: boolean;
  onUpdate?: (editor: Editor) => void;
}) => {
  const { t } = useI18n("knowledge");
  const editor = useEditor({
    extensions: editorExtensions(t("editorPlaceholder"), {
      articleId,
      moduleName: (module) => t(recordModules[module]),
    }),
    content,
    editable,
    immediatelyRender: true,
    shouldRerenderOnTransaction: false,
    // A replaced document (setContent with emitUpdate false) does not call this: only typing does.
    // Nor does the editor's own bookkeeping kept out of the undo history, such as the
    // block ids given to an older document when it opens (K18): that is not an edit.
    onUpdate: ({ editor: current, transaction }) => {
      if (transaction.getMeta("addToHistory") === false) return;
      onUpdate?.(current);
    },
  });
  useEffect(() => {
    if (editor && editor.isEditable !== editable) editor.setEditable(editable, false);
  }, [editor, editable]);
  return editor;
};
