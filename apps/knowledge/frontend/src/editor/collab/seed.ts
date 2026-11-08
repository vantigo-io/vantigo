import { Editor, type Extensions } from "@tiptap/core";
import { prosemirrorJSONToYDoc } from "@tiptap/y-tiptap";
import * as Y from "yjs";
import { blockIdTypes, COLLAB_FIELD } from "../extensions";

/**
 * The whole Yjs state of a stored document (knowledge design K29). The JSON first goes through a
 * throwaway editor with the page's extensions (but not `Collaboration`), so every block has
 * the id K18 gives it and the seed matches what an editor would make of the same text; then
 * the document is built from the editor's own JSON.
 *
 * The editor's own id pass runs after its creation, on a timer, which is too late for a seed
 * that is built at once: the ids are given here, to the blocks that lack one, as it does.
 */
export const seedState = (content: unknown, extensions: Extensions): Uint8Array => {
  const editor = new Editor({ extensions, content: content as Record<string, unknown> });
  try {
    const { state, view } = editor;
    const tr = state.tr;
    const ids: readonly string[] = blockIdTypes;
    state.doc.descendants((node, pos) => {
      if (ids.includes(node.type.name) && node.attrs.id == null) {
        tr.setNodeMarkup(pos, undefined, { ...node.attrs, id: crypto.randomUUID() });
      }
    });
    if (tr.docChanged) {
      tr.setMeta("addToHistory", false);
      view.dispatch(tr);
    }
    const doc = prosemirrorJSONToYDoc(editor.schema, editor.getJSON(), COLLAB_FIELD);
    return Y.encodeStateAsUpdate(doc);
  } finally {
    editor.destroy();
  }
};
