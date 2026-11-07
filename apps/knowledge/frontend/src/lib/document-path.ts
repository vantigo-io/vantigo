/** A document node as JSON, as far as a path through it needs. */
interface JsonNode {
  type?: string;
  content?: JsonNode[];
}

/**
 * The node types a refusal's JSON path (`$.content[2].content[0].attrs.src`)
 * passes through in the document, from the root down (knowledge design K8), so
 * the page can say which kind of block the server refused.
 */
export const nodeTypesOnPath = (doc: JsonNode | null | undefined, path: string): string[] => {
  const types: string[] = [];
  let current: JsonNode | undefined = doc ?? undefined;
  const steps = [...path.matchAll(/\.(\w+)|\[(\d+)\]/g)];
  for (let i = 0; i < steps.length && current; i += 1) {
    const key = steps[i][1];
    if (key !== "content") break;
    const index = steps[i + 1]?.[2];
    if (index === undefined) break;
    current = current.content?.[Number(index)];
    if (current?.type) types.push(current.type);
    i += 1;
  }
  return types;
};

/** Whether the refused part of the document is an image or a file chip (K19). */
export const pathIsFile = (doc: JsonNode | null | undefined, path: string) =>
  nodeTypesOnPath(doc, path).some((type) => type === "image" || type === "fileAttachment");
