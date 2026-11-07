import { describe, expect, it } from "vitest";
import { nodeTypesOnPath, pathIsFile } from "./document-path";

const doc = {
  type: "doc",
  content: [
    { type: "paragraph" },
    { type: "callout", content: [{ type: "image" }, { type: "paragraph", content: [{ type: "text" }] }] },
  ],
};

describe("a refusal's path through the document", () => {
  it("names the blocks it passes through", () => {
    expect(nodeTypesOnPath(doc, "$.content[1].content[0].attrs.src")).toEqual(["callout", "image"]);
    expect(nodeTypesOnPath(doc, "$.content[1].content[1].content[0].marks[0].attrs.href")).toEqual([
      "callout",
      "paragraph",
      "text",
    ]);
    expect(nodeTypesOnPath(doc, "$.content[9]")).toEqual([]);
  });

  it("tells a refused picture or file from other blocks", () => {
    expect(pathIsFile(doc, "$.content[1].content[0].attrs.src")).toBe(true);
    expect(pathIsFile(doc, "$.content[0].attrs.id")).toBe(false);
    expect(pathIsFile(null, "$.content[0]")).toBe(false);
  });
});
