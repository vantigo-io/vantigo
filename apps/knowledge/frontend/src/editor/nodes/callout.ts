import { mergeAttributes, Node } from "@tiptap/core";
import { ReactNodeViewRenderer } from "@tiptap/react";
import { type CalloutKind, isCalloutKind } from "./callout-kinds";
import { CalloutView } from "./callout-view";

declare module "@tiptap/core" {
  interface Commands<ReturnType> {
    callout: {
      /** Wraps the selected blocks in a callout of a kind. */
      setCallout: (kind?: CalloutKind) => ReturnType;
    };
  }
}

/**
 * A callout (knowledge design K18): blocks in a coloured box of a kind (info,
 * tip, warning or danger) with an optional emoji. Stored as
 * `{ type: "callout", attrs: { kind, emoji } }`.
 */
export const Callout = Node.create({
  name: "callout",
  group: "block",
  content: "block+",
  defining: true,

  addAttributes() {
    return {
      kind: {
        default: "info",
        parseHTML: (element) => {
          const value = element.getAttribute("data-kind");
          return isCalloutKind(value) ? value : "info";
        },
        renderHTML: (attributes) => ({ "data-kind": attributes.kind }),
      },
      emoji: {
        default: null,
        parseHTML: (element) => element.getAttribute("data-emoji") || null,
        renderHTML: (attributes) => (attributes.emoji ? { "data-emoji": attributes.emoji } : {}),
      },
    };
  },

  parseHTML() {
    return [{ tag: 'div[data-type="callout"]' }];
  },

  renderHTML({ HTMLAttributes }) {
    return ["div", mergeAttributes(HTMLAttributes, { "data-type": "callout" }), 0];
  },

  addNodeView() {
    return ReactNodeViewRenderer(CalloutView);
  },

  addCommands() {
    return {
      setCallout:
        (kind = "info") =>
        ({ commands }) =>
          commands.wrapIn(this.name, { kind }),
    };
  },
});
