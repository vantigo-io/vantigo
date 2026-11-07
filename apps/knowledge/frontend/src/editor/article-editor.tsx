import "@mantine/tiptap/styles.css";
import { RichTextEditor } from "@mantine/tiptap";
import type { Editor } from "@tiptap/react";
import { BubbleMenu } from "@tiptap/react/menus";
import { useI18n } from "@vantigo/frontend-shell";

/** The editor's surface, with a bubble menu for formatting a selection while it is editable. */
export const ArticleEditor = ({
  editor,
  editable,
  label,
}: {
  editor: Editor | null;
  editable: boolean;
  label: string;
}) => {
  const { t } = useI18n("knowledge");
  if (!editor) return null;
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
      <RichTextEditor.Content aria-label={label} />
    </RichTextEditor>
  );
};
