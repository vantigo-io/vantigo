import Link from "@tiptap/extension-link";
import TaskItem from "@tiptap/extension-task-item";
import TaskList from "@tiptap/extension-task-list";
import { Placeholder } from "@tiptap/extensions";
import StarterKit from "@tiptap/starter-kit";

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
 * The editor's extensions in K-A (K7): StarterKit with headings 1–3 (its own
 * Markdown input rules: `#`, `-`, `1.`, `>`, three backticks), Underline kept,
 * Link (configured here, so StarterKit's own is off), task lists (`[ ]`) and
 * the placeholder. Its schema is what `schema.json` records for the server's
 * allowlist test; a change here is a change there.
 */
export const editorExtensions = (placeholder = "") => [
  StarterKit.configure({ heading: { levels: [1, 2, 3] }, link: false }),
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
  Placeholder.configure({ placeholder }),
];
