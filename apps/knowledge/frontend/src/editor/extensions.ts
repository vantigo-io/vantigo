import { Details, DetailsContent, DetailsSummary } from "@tiptap/extension-details";
import Link from "@tiptap/extension-link";
import TaskItem from "@tiptap/extension-task-item";
import TaskList from "@tiptap/extension-task-list";
import UniqueID from "@tiptap/extension-unique-id";
import { Placeholder } from "@tiptap/extensions";
import StarterKit from "@tiptap/starter-kit";
import type { RecordModule } from "../lib/record-modules";
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
  { articleId = null, moduleName }: { articleId?: string | null; moduleName?: (module: RecordModule) => string } = {},
) => [
  StarterKit.configure({ heading: { levels: [1, 2, 3] }, link: false, codeBlock: false }),
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
