import "@mantine/tiptap/styles.css";
import "./collab/carets.css";
import { RichTextEditor } from "@mantine/tiptap";
import type { Editor } from "@tiptap/react";
import { BubbleMenu } from "@tiptap/react/menus";
import { useI18n } from "@vantigo/frontend-shell";
import { useRef } from "react";
import { BlockHandle } from "./block-handle";
import { ArticleLinkPicker, MentionPicker } from "./pickers";
import { SlashMenu } from "./slash-menu";
import { TableMenu } from "./table-menu";
import { UploadList } from "./upload-list";

/** What the slash menu's Image and File offer the file picker (K19): images are only JPEG and PNG. */
const accepts = { image: "image/jpeg,image/png", file: "" } as const;

/**
 * The editor's surface. While it is editable: a bubble menu for formatting a
 * selection, the block handle (`+` and drag), the slash menu, the `@` and
 * `[[` pickers, the table's cell menu (knowledge design K18, K20, K21) and the
 * uploads under way (K19).
 */
export const ArticleEditor = ({
  editor,
  editable,
  label,
  articleId,
}: {
  editor: Editor | null;
  editable: boolean;
  label: string;
  /** The article being edited, which uploads go to (K19). */
  articleId?: string;
}) => {
  const { t } = useI18n("knowledge");
  const picker = useRef<HTMLInputElement>(null);
  if (!editor) return null;
  const pickFile = (accept: "image" | "file") => {
    const input = picker.current;
    if (!input) return;
    input.accept = accepts[accept];
    input.click();
  };
  return (
    <RichTextEditor
      editor={editor}
      variant="subtle"
      styles={{ root: { border: 0 }, content: { backgroundColor: "transparent" } }}
      labels={{
        boldControlLabel: t("formatBold"),
        italicControlLabel: t("formatItalic"),
        underlineControlLabel: t("formatUnderline"),
        strikeControlLabel: t("formatStrike"),
        codeControlLabel: t("formatCode"),
        linkControlLabel: t("formatLink"),
        unlinkControlLabel: t("formatUnlink"),
        h1ControlLabel: t("formatH1"),
        h2ControlLabel: t("formatH2"),
        h3ControlLabel: t("formatH3"),
        bulletListControlLabel: t("formatBulletList"),
        orderedListControlLabel: t("formatOrderedList"),
        tasksControlLabel: t("formatTaskList"),
        blockquoteControlLabel: t("formatQuote"),
        linkEditorInputLabel: t("linkLabel"),
        linkEditorInputPlaceholder: t("linkPlaceholder"),
        linkEditorExternalLink: t("linkExternal"),
        linkEditorInternalLink: t("linkInternal"),
        linkEditorSave: t("save"),
      }}
    >
      {editable && (
        <BubbleMenu editor={editor}>
          <RichTextEditor.ControlsGroup>
            <RichTextEditor.Bold />
            <RichTextEditor.Italic />
            <RichTextEditor.Underline />
            <RichTextEditor.Strikethrough />
            <RichTextEditor.Code />
            <RichTextEditor.Link />
            <RichTextEditor.Unlink />
            <RichTextEditor.H1 />
            <RichTextEditor.H2 />
            <RichTextEditor.H3 />
            <RichTextEditor.BulletList />
            <RichTextEditor.OrderedList />
            <RichTextEditor.TaskList />
            <RichTextEditor.Blockquote />
          </RichTextEditor.ControlsGroup>
        </BubbleMenu>
      )}
      {editable && (
        <>
          <BlockHandle editor={editor} />
          <SlashMenu editor={editor} onUpload={articleId ? pickFile : undefined} />
          <MentionPicker editor={editor} />
          <ArticleLinkPicker editor={editor} articleId={articleId} />
          <TableMenu editor={editor} />
          <UploadList editor={editor} />
          <input
            ref={picker}
            type="file"
            multiple
            hidden
            data-testid="knowledge-file-picker"
            onChange={(event) => {
              const files = Array.from(event.currentTarget.files ?? []);
              event.currentTarget.value = "";
              if (files.length > 0) editor.storage.knowledgeUploads.start(files, editor.state.selection.from);
            }}
          />
        </>
      )}
      <RichTextEditor.Content aria-label={label} />
    </RichTextEditor>
  );
};
