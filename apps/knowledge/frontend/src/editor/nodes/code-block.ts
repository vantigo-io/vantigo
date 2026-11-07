import CodeBlockLowlight from "@tiptap/extension-code-block-lowlight";
import { ReactNodeViewRenderer } from "@tiptap/react";
import { common, createLowlight } from "lowlight";
import { CodeBlockView } from "./code-block-view";

/** Highlighting for every language in lowlight's common set, which `codeLanguages` is drawn from. */
const lowlight = createLowlight(common);

/**
 * Code blocks with syntax highlighting (K18): StarterKit's `codeBlock` with
 * lowlight, the language chosen per block. A block without one is detected.
 */
export const CodeBlock = CodeBlockLowlight.extend({
  addNodeView() {
    return ReactNodeViewRenderer(CodeBlockView);
  },
}).configure({ lowlight, enableTabIndentation: true });
