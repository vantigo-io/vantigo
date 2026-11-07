import { mergeAttributes, Node } from "@tiptap/core";
import { ReactNodeViewRenderer } from "@tiptap/react";
import { fileIdOfSrc } from "../../api/files";
import { FileAttachmentView, ImageView } from "./files-view";

/**
 * An image (knowledge design K19): one of the article's own files, its
 * `src` exactly `/api/v1/knowledge/files/<id>`, as the allowlist requires.
 */
export const KnowledgeImage = Node.create({
  name: "image",
  group: "block",
  atom: true,
  draggable: true,

  addAttributes() {
    return {
      src: { default: null },
      alt: { default: null },
      title: { default: null },
      width: { default: null },
    };
  },

  parseHTML() {
    return [
      {
        tag: "img[src]",
        getAttrs: (element) => (fileIdOfSrc(element.getAttribute("src") ?? "") ? null : false),
      },
    ];
  },

  renderHTML({ HTMLAttributes }) {
    return ["img", mergeAttributes(HTMLAttributes)];
  },

  addNodeView() {
    return ReactNodeViewRenderer(ImageView);
  },
});

/**
 * A file attachment (K19): any file that is not a JPEG or a PNG, SVG
 * included, shown as a chip that downloads it.
 */
export const FileAttachment = Node.create({
  name: "fileAttachment",
  group: "block",
  atom: true,
  draggable: true,

  addAttributes() {
    return {
      fileId: { default: null },
      filename: { default: null },
      size: { default: null },
      contentType: { default: null },
    };
  },

  parseHTML() {
    return [{ tag: 'div[data-type="fileAttachment"]' }];
  },

  renderHTML({ HTMLAttributes }) {
    return ["div", mergeAttributes(HTMLAttributes, { "data-type": "fileAttachment" })];
  },

  addNodeView() {
    return ReactNodeViewRenderer(FileAttachmentView);
  },
});
