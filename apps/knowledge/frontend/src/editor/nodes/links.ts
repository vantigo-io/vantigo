import { Extension, mergeAttributes, Node } from "@tiptap/core";
import { Plugin, PluginKey } from "@tiptap/pm/state";
import { ReactNodeViewRenderer } from "@tiptap/react";
import { appConfig, runtimeBase } from "@vantigo/frontend-shell";
import { enabledRecordModule, type RecordModule } from "../../lib/record-modules";
import { ArticleLinkView, RecordLinkView } from "./links-view";

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/**
 * A link to another article (knowledge design K21): an inline atom holding
 * the target's id and the title it had when it was written. It shows the
 * current title, or "No access", or "Deleted", and opens the article.
 */
export const ArticleLink = Node.create({
  name: "articleLink",
  group: "inline",
  inline: true,
  atom: true,
  selectable: true,

  addAttributes() {
    return { id: { default: null }, label: { default: null, renderHTML: () => ({}) } };
  },

  parseHTML() {
    return [{ tag: 'span[data-type="articleLink"]' }];
  },

  // The stored label is never shown (K21): only the title the reader may see, resolved live.
  renderHTML({ HTMLAttributes }) {
    return ["span", mergeAttributes(HTMLAttributes, { "data-type": "articleLink" })];
  },

  renderText() {
    return "";
  },

  addNodeView() {
    return ReactNodeViewRenderer(ArticleLinkView);
  },
});

/**
 * A link to a Vantigo record (K22): an inline atom holding the app path of
 * a customer, a project, an order… Opening it is an ordinary navigation, so it
 * shows only what the person may open.
 */
export const RecordLink = Node.create({
  name: "recordLink",
  group: "inline",
  inline: true,
  atom: true,
  selectable: true,

  addAttributes() {
    return { path: { default: null }, label: { default: null } };
  },

  parseHTML() {
    return [{ tag: 'span[data-type="recordLink"]' }];
  },

  renderHTML({ HTMLAttributes, node }) {
    return ["span", mergeAttributes(HTMLAttributes, { "data-type": "recordLink" }), node.attrs.label ?? ""];
  },

  renderText({ node }) {
    return node.attrs.label ?? node.attrs.path ?? "";
  },

  addNodeView() {
    return ReactNodeViewRenderer(RecordLinkView);
  },
});

const articlePath = /^\/knowledge\/articles\/([0-9a-f-]{36})\/?$/i;

/**
 * What a pasted text becomes when it is one URL of this Vantigo (K21, K22):
 * an article's address an article link; a path under an enabled module a
 * record chip, labelled with the module's name and the last segment that
 * names something (an id shortened). Anything else — another site, a query
 * string or fragment, a route outside the modules — stays ordinary text.
 */
export const linkNodeForUrl = (
  text: string,
  {
    origin = window.location.origin,
    base = runtimeBase(),
    enabled,
    moduleName = (module: RecordModule) => module,
  }: {
    origin?: string;
    base?: string;
    enabled?: readonly string[];
    moduleName?: (module: RecordModule) => string;
  } = {},
) => {
  const value = text.trim();
  if (!value || /\s/.test(value)) return null;
  let url: URL;
  try {
    url = new URL(value);
  } catch {
    return null;
  }
  if (url.origin !== origin || url.search || url.hash || value.includes("#")) return null;
  const prefix = base.endsWith("/") ? base.slice(0, -1) : base;
  if (prefix && url.pathname !== prefix && !url.pathname.startsWith(`${prefix}/`)) return null;
  const path = url.pathname.slice(prefix.length).replace(/\/+$/, "") || "/";
  const article = articlePath.exec(path);
  if (article) return { type: "articleLink", attrs: { id: article[1].toLowerCase(), label: null } };
  if (!/^\/[A-Za-z0-9_-][A-Za-z0-9/_-]{0,499}$/.test(path) || path.includes("//")) return null;
  const segments = path.split("/").filter(Boolean);
  const module = enabledRecordModule(segments[0] ?? "", enabled ?? appConfig().modules);
  if (!module) return null;
  const last = segments.length > 1 ? segments.at(-1) : undefined;
  const detail = last ? (uuid.test(last) ? last.slice(0, 8) : decodeURIComponent(last).replace(/[-_]+/g, " ")) : null;
  const label = [moduleName(module), detail].filter(Boolean).join(" · ");
  return { type: "recordLink", attrs: { path, label: label.slice(0, 200) || null } };
};

/** Turns a pasted Vantigo URL into a link chip (K21, K22). */
export const VantigoLinkPaste = Extension.create<{ moduleName: (module: RecordModule) => string }>({
  name: "vantigoLinkPaste",
  addOptions: () => ({ moduleName: (module: RecordModule) => module }),
  addProseMirrorPlugins() {
    const { editor } = this;
    const { moduleName } = this.options;
    return [
      new Plugin({
        key: new PluginKey("vantigoLinkPaste"),
        props: {
          handlePaste: (view, event) => {
            if (!view.editable || (event.clipboardData?.files.length ?? 0) > 0) return false;
            const node = linkNodeForUrl(event.clipboardData?.getData("text/plain") ?? "", { moduleName });
            if (!node) return false;
            if (view.state.selection.$from.parent.type.name === "codeBlock") return false;
            editor
              .chain()
              .focus()
              .insertContent([node, { type: "text", text: " " }])
              .run();
            return true;
          },
        },
      }),
    ];
  },
});
