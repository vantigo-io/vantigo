import { type Editor, Extension, type JSONContent } from "@tiptap/core";
import { Fragment, type Node as PMNode, Slice } from "@tiptap/pm/model";
import { Plugin, PluginKey } from "@tiptap/pm/state";
import { copyFile, fileIdOfSrc, type KnowledgeFile, MAX_FILE_BYTES, uploadFile } from "../api/files";
import { NotFoundError, refusalCode } from "../api/request";

/** Why an upload failed, as the editor says it: a catalog key's suffix. */
export type UploadProblem = "tooLarge" | "locked" | "lockedMidway" | "readOnly" | "storage" | "notFound" | "failed";

/** An upload under way, or one that failed and waits to be dismissed. */
export interface Upload {
  id: string;
  name: string;
  progress: number;
  problem: UploadProblem | null;
}

const problemOf = (error: unknown): UploadProblem => {
  const code = refusalCode(error);
  if (code === "file_too_large") return "tooLarge";
  if (code === "article_locked") return "locked";
  if (code === "article_read_only") return "readOnly";
  if (code === "storage_unavailable") return "storage";
  if (error instanceof NotFoundError) return "notFound";
  return "failed";
};

/** The node an uploaded file becomes (K19): a JPEG or a PNG an image, anything else a file chip. */
export const nodeForFile = (file: KnowledgeFile): JSONContent =>
  file.isImage
    ? { type: "image", attrs: { src: file.url, alt: file.filename } }
    : {
        type: "fileAttachment",
        attrs: { fileId: file.id, filename: file.filename, size: file.size, contentType: file.contentType },
      };

/** The editor's uploads (K19), a small store the upload list reads. */
export class UploadStore {
  private list: Upload[] = [];
  private readonly listeners = new Set<() => void>();

  readonly get = () => this.list;

  readonly subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };

  set(next: Upload[]) {
    this.list = next;
    for (const listener of this.listeners) listener();
  }

  update(id: string, change: Partial<Upload>) {
    this.set(this.list.map((upload) => (upload.id === id ? { ...upload, ...change } : upload)));
  }

  readonly dismiss = (id: string) => this.set(this.list.filter((upload) => upload.id !== id));
}

declare module "@tiptap/core" {
  interface Storage {
    knowledgeUploads: {
      store: UploadStore;
      /** The files this article's document has named while open: pasting one of them again copies nothing. */
      known: Set<string>;
      /** Aborts every upload and copy under way when the editor is destroyed. */
      controller: AbortController;
      /** Uploads the files into the article, each inserted at `pos` (mapped through later edits) when it is done. */
      start: (files: File[], pos?: number) => void;
    };
  }
}

let sequence = 0;

interface UploadContext {
  editor: Editor;
  store: UploadStore;
  /** Aborted when the editor goes away, so nothing lands in a page that is no longer open. */
  signal: AbortSignal;
}

/**
 * Runs one upload or copy into the article and puts its node at `at`, kept
 * current while the person goes on typing. A page locked meanwhile gets
 * nothing, and says so.
 */
const runInto = (
  { editor, store, signal }: UploadContext,
  name: string,
  at: number,
  work: (options: { signal: AbortSignal; onProgress: (fraction: number) => void }) => Promise<KnowledgeFile>,
) => {
  sequence += 1;
  const id = `upload-${sequence}`;
  store.set([...store.get(), { id, name, progress: 0, problem: null }]);
  let pos = at;
  const follow = ({ transaction }: { transaction: { mapping: { map: (pos: number) => number } } }) => {
    pos = transaction.mapping.map(pos);
  };
  editor.on("transaction", follow);
  work({ signal, onProgress: (progress) => store.update(id, { progress }) })
    .then((file) => {
      if (editor.isDestroyed || signal.aborted) return;
      if (!editor.isEditable) {
        store.update(id, { problem: "lockedMidway" });
        return;
      }
      editor.storage.knowledgeUploads.known.add(file.id);
      editor.chain().insertContentAt(Math.min(pos, editor.state.doc.content.size), nodeForFile(file)).run();
      store.dismiss(id);
    })
    .catch((error: unknown) => {
      if (signal.aborted || (error instanceof DOMException && error.name === "AbortError")) return;
      store.update(id, { problem: problemOf(error) });
    })
    .finally(() => editor.off("transaction", follow));
};

const startUploads = (context: UploadContext, articleId: string | null, files: File[], at?: number) => {
  const { editor, store } = context;
  if (!articleId || !editor.isEditable) return;
  for (const file of files) {
    if (file.size > MAX_FILE_BYTES) {
      sequence += 1;
      store.set([...store.get(), { id: `upload-${sequence}`, name: file.name, progress: 0, problem: "tooLarge" }]);
      continue;
    }
    runInto(context, file.name, at ?? editor.state.selection.from, (options) => uploadFile(articleId, file, options));
  }
};

/** The file an image or a file chip names, if it is one. */
export const fileIdOfNode = (node: PMNode): string | null => {
  if (node.type.name === "image") return fileIdOfSrc(String(node.attrs.src ?? ""));
  if (node.type.name === "fileAttachment") return typeof node.attrs.fileId === "string" ? node.attrs.fileId : null;
  return null;
};

/** Every file the document names. */
const fileIdsOf = (doc: PMNode) => {
  const found: string[] = [];
  doc.descendants((node) => {
    const id = fileIdOfNode(node);
    if (id) found.push(id);
  });
  return found;
};

/**
 * A pasted or dropped slice without the images and files of another article
 * (K19: a document names only its own article's files), and those it took
 * out, in order, to copy into this one.
 */
export const withoutForeignFiles = (slice: Slice, known: ReadonlySet<string>) => {
  const foreign: { node: PMNode; fileId: string }[] = [];
  const strip = (fragment: Fragment): Fragment => {
    const kept: PMNode[] = [];
    fragment.forEach((node) => {
      const id = fileIdOfNode(node);
      if (id && !known.has(id)) {
        foreign.push({ node, fileId: id });
        return;
      }
      kept.push(node.isLeaf ? node : node.copy(strip(node.content)));
    });
    return Fragment.fromArray(kept);
  };
  const content = strip(slice.content);
  return { slice: foreign.length ? new Slice(content, slice.openStart, slice.openEnd) : slice, foreign };
};

const filesOf = (list: FileList | null | undefined) => Array.from(list ?? []);

/**
 * Images and files in an article (knowledge design K19): pasting or dropping
 * files, or choosing Image or File in the slash menu, uploads them into the
 * article; each becomes an image (JPEG, PNG) or a file chip where it was put.
 */
export const KnowledgeUploads = Extension.create<{ articleId: string | null }>({
  name: "knowledgeUploads",
  addOptions: () => ({ articleId: null }),
  addStorage() {
    return {
      store: new UploadStore(),
      known: new Set<string>(),
      controller: new AbortController(),
      // Replaced in onCreate, once the editor exists.
      start: (() => undefined) as (files: File[], pos?: number) => void,
    };
  },
  onCreate() {
    const context = { editor: this.editor, store: this.storage.store, signal: this.storage.controller.signal };
    this.storage.start = (files: File[], pos?: number) => startUploads(context, this.options.articleId, files, pos);
    for (const id of fileIdsOf(this.editor.state.doc)) this.storage.known.add(id);
  },
  onTransaction({ transaction }) {
    if (transaction.docChanged) for (const id of fileIdsOf(transaction.doc)) this.storage.known.add(id);
  },
  onDestroy() {
    this.storage.controller.abort();
  },
  addProseMirrorPlugins() {
    const start = (files: File[], pos?: number) => this.storage.start(files, pos);
    const { editor } = this;
    const storage = this.storage;
    const articleId = this.options.articleId;
    return [
      new Plugin({
        key: new PluginKey("knowledgeUploads"),
        props: {
          // An image or file from another article is copied into this one (K19) and put
          // where it was pasted when the copy answers; its old id never reaches a save.
          transformPasted: (slice, view) => {
            const { slice: kept, foreign } = withoutForeignFiles(slice, storage.known);
            if (foreign.length === 0) return slice;
            const context = { editor, store: storage.store, signal: storage.controller.signal };
            const at = view.state.selection.from;
            for (const { node, fileId } of foreign) {
              const name = String(node.attrs.filename ?? node.attrs.alt ?? "") || fileId;
              if (!articleId) continue;
              runInto(context, name, at, ({ signal }) => copyFile(articleId, fileId, { signal }));
            }
            return kept;
          },
          handlePaste: (view, event) => {
            const files = filesOf(event.clipboardData?.files);
            if (files.length === 0 || !view.editable) return false;
            start(files, view.state.selection.from);
            return true;
          },
          handleDrop: (view, event, _slice, moved) => {
            if (moved || !view.editable) return false;
            const files = filesOf(event.dataTransfer?.files);
            if (files.length === 0) return false;
            event.preventDefault();
            const at = view.posAtCoords({ left: event.clientX, top: event.clientY });
            start(files, at?.pos ?? view.state.selection.from);
            return true;
          },
        },
      }),
    ];
  },
});
