import { getSchema } from "@tiptap/react";
import { editorExtensions } from "./extensions";

/** What the server's allowlist mirrors (knowledge design K8): every node and mark type, and its attributes' names. */
export interface ExportedSchema {
  nodes: Record<string, { attrs: string[] }>;
  marks: Record<string, { attrs: string[] }>;
}

type Entry = [string, { attrs: string[] }];

const sorted = (entries: Entry[]) => Object.fromEntries(entries.sort(([a], [b]) => a.localeCompare(b)));

/** The editor's ProseMirror schema as names and attribute names, sorted, so the file only changes when the schema does. */
export const exportSchema = (): ExportedSchema => {
  const schema = getSchema(editorExtensions());
  const nodes: Entry[] = [];
  schema.spec.nodes.forEach((name, spec) => {
    nodes.push([name, { attrs: Object.keys(spec.attrs ?? {}).sort() }]);
  });
  const marks: Entry[] = [];
  schema.spec.marks.forEach((name, spec) => {
    marks.push([name, { attrs: Object.keys(spec.attrs ?? {}).sort() }]);
  });
  return { nodes: sorted(nodes), marks: sorted(marks) };
};
