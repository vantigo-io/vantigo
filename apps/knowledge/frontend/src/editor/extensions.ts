import Collaboration from "@tiptap/extension-collaboration";
import CollaborationCaret from "@tiptap/extension-collaboration-caret";
import { Details, DetailsContent, DetailsSummary } from "@tiptap/extension-details";
import Link from "@tiptap/extension-link";
import TaskItem from "@tiptap/extension-task-item";
import TaskList from "@tiptap/extension-task-list";
import UniqueID from "@tiptap/extension-unique-id";
import { Placeholder } from "@tiptap/extensions";
import StarterKit from "@tiptap/starter-kit";
import type { RecordModule } from "../lib/record-modules";
import type { CollabProvider } from "./collab/provider";
import { CURSOR_COLOURS, colourOf } from "./collab/user";
import { Callout } from "./nodes/callout";
import { CodeBlock } from "./nodes/code-block";
import { FileAttachment, KnowledgeImage } from "./nodes/files";
import { ArticleLink, RecordLink, VantigoLinkPaste } from "./nodes/links";
import { KnowledgeMention } from "./nodes/mention";
import { tableExtensions } from "./nodes/table";
import { TableOfContents } from "./nodes/table-of-contents";
import { ArticleLinkPickerExtension, SlashMenuExtension } from "./suggestions";
import { KnowledgeUploads } from "./uploads";

/**
 * A link's target as the server's allowlist takes it (knowledge design K8):
 * `https:`, `http:`, `mailto:` or an application path starting with `/`. The
 * editor refuses any other, so a pasted `javascript:` link never reaches a save.
 */
export const isAllowedHref = (href: string): boolean => {
  const value = href.trim();
  if (value.startsWith("/") && !value.startsWith("//")) return true;
  return /^(https?:|mailto:)/i.test(value);
};

/**
 * The block types that carry a stable `id` (knowledge design K18), which the
 * table of contents links to and K-C's cursors and anchors reuse.
 */
export const blockIdTypes = [
  "paragraph",
  "heading",
  "bulletList",
  "orderedList",
  "taskList",
  "listItem",
  "taskItem",
  "blockquote",
  "codeBlock",
  "horizontalRule",
  "callout",
  "details",
  "table",
  "tableOfContents",
  "image",
  "fileAttachment",
] as const;

/** The name of the Yjs fragment the editor's `Collaboration` extension binds to. */
export const COLLAB_FIELD = "default";

/**
 * What another person's awareness may put on this screen. The state is whatever a writer's
 * browser sent, so the name is cut and the colour is one of ours or derived from the id: never a
 * string that reaches a style attribute as sent. (The server does not stamp identities yet; see the reference.)
 */
const MAX_CARET_NAME = 60;
export const safeCaretUser = (user: Record<string, unknown>): { name: string; color: string } => {
  const color = (CURSOR_COLOURS as readonly string[]).includes(String(user.color))
    ? String(user.color)
    : colourOf(String(user.id ?? user.name ?? ""));
  return { name: String(user.name ?? "").slice(0, MAX_CARET_NAME), color };
};

/** Another person's cursor: a bar in their colour with their name above it (K30). */
const renderCaret = (raw: Record<string, unknown>): HTMLElement => {
  const user = safeCaretUser(raw);
  const caret = document.createElement("span");
  caret.classList.add("collaboration-carets__caret");
  caret.style.borderColor = user.color;
  const label = document.createElement("div");
  label.classList.add("collaboration-carets__label");
  label.style.backgroundColor = user.color;
  label.textContent = user.name;
  caret.append(label);
  return caret;
};

const renderSelection = (raw: Record<string, unknown>) => {
  const user = safeCaretUser(raw);
  return {
    nodeName: "span",
    class: "collaboration-carets__selection",
    "data-user": user.name,
    style: `background-color: ${user.color}33`,
  };
};

/**
 * The editor's extensions. K-A (K7): StarterKit with headings 1–3 (its own
 * Markdown input rules: `#`, `-`, `1.`, `>`, three backticks), Underline kept,
 * Link (configured here, so StarterKit's own is off), task lists (`[ ]`) and
 * the placeholder. K-B (K18): code blocks with lowlight (so StarterKit's own
 * is off), callouts, toggles (`details`, its open state kept), tables, the
 * table of contents and block ids; images and file attachments uploaded into
 * `articleId` (K19); mentions (K20); article links from `[[` (K21) and record
 * chips from a pasted Vantigo URL (K22); the slash menu. Its schema is what
 * `schema.json` records for the server's allowlist test; a change here is a
 * change there.
 */
export const editorExtensions = (
  placeholder = "",
  {
    articleId = null,
    moduleName,
    collab,
  }: {
    articleId?: string | null;
    moduleName?: (module: RecordModule) => string;
    /** Live co-editing (K-C, K33): the editor is bound to the provider's document, with its own undo history and the others' cursors. */
    collab?: { provider: CollabProvider; user: { name: string; color: string } };
  } = {},
) => [
  // With `Collaboration` the undo history is Yjs's (a person undoes only their own changes), so StarterKit's is off.
  StarterKit.configure({
    heading: { levels: [1, 2, 3] },
    link: false,
    codeBlock: false,
    undoRedo: collab ? false : undefined,
  }),
  ...(collab
    ? [
        Collaboration.configure({ document: collab.provider.doc, field: COLLAB_FIELD }),
        CollaborationCaret.configure({
          provider: collab.provider,
          user: collab.user,
          render: renderCaret,
          selectionRender: renderSelection,
        }),
      ]
    : []),
  Link.configure({
    openOnClick: false,
    autolink: true,
    defaultProtocol: "https",
    protocols: ["http", "https", "mailto"],
    isAllowedUri: (url) => isAllowedHref(url),
    shouldAutoLink: (url) => isAllowedHref(url) || /^[\w.-]+\.[a-z]{2,}/i.test(url),
  }),
  TaskList,
  TaskItem.configure({ nested: true }),
  CodeBlock,
  Callout,
  Details.configure({ persist: true }),
  DetailsSummary,
  DetailsContent,
  ...tableExtensions,
  TableOfContents,
  KnowledgeImage,
  FileAttachment,
  KnowledgeMention,
  ArticleLink,
  RecordLink,
  UniqueID.configure({ types: [...blockIdTypes] }),
  SlashMenuExtension,
  ArticleLinkPickerExtension,
  moduleName ? VantigoLinkPaste.configure({ moduleName }) : VantigoLinkPaste,
  KnowledgeUploads.configure({ articleId }),
  Placeholder.configure({ placeholder, includeChildren: false }),
];
