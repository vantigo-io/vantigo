import { describe, expect, it } from "vitest";
import { visibleKnowledgeNavItems } from "./navigation";

describe("the Knowledge sidebar", () => {
  it("offers the articles and the trash to knowledge:articles, and nothing without it", () => {
    expect(visibleKnowledgeNavItems(["knowledge:articles"]).map((item) => item.key)).toEqual(["articles", "trash"]);
    expect(visibleKnowledgeNavItems(["*"]).map((item) => item.key)).toEqual(["articles", "trash"]);
    expect(visibleKnowledgeNavItems(["knowledge:manage"])).toEqual([]);
    expect(visibleKnowledgeNavItems(undefined)).toEqual([]);
  });
});
