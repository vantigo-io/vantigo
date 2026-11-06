import { describe, expect, it } from "vitest";
import {
  addOption,
  addYesNo,
  canReplyInWindow,
  characters,
  defaultComponentLimits,
  isChat,
  limitsOf,
  optionsProblem,
  textWithinLimit,
  uniqueOptionId,
  validOptionTitle,
} from "./chat";

const limits = defaultComponentLimits;
const yesNo = { yes: "Yes", no: "No" };

describe("a Messenger composer's answer options", () => {
  it("adds Yes and No together, in the user's words, with ids that stay unique", () => {
    const once = addYesNo([], { yes: "Ja", no: "Nei" }, limits);
    expect(once).toEqual([
      { id: "yes", title: "Ja" },
      { id: "no", title: "Nei" },
    ]);
    const twice = addYesNo(once, yesNo, limits);
    expect(twice.map((option) => option.id)).toEqual(["yes", "no", "yes-2", "no-2"]);
  });

  it("keeps the order the options were added in", () => {
    const options = addYesNo(addOption([], limits), yesNo, limits);
    expect(options.map((option) => option.id)).toEqual(["option", "yes", "no"]);
  });

  it("adds an option up to the provider's limit and no further", () => {
    let options = addOption([], limits);
    for (let i = 1; i < limits.maxAnswerOptions; i++) options = addOption(options, limits);
    expect(options).toHaveLength(13);
    expect(new Set(options.map((option) => option.id)).size).toBe(13);
    expect(addOption(options, limits)).toHaveLength(13);
  });

  it("does not add Yes / No when the two do not fit", () => {
    let options = addOption([], limits);
    for (let i = 1; i < limits.maxAnswerOptions - 1; i++) options = addOption(options, limits);
    expect(options).toHaveLength(12);
    expect(addYesNo(options, yesNo, limits)).toHaveLength(12);
  });

  it("takes a title of 1 to 20 characters once trimmed, counting characters and not bytes", () => {
    expect(validOptionTitle("", limits)).toBe(false);
    expect(validOptionTitle("   ", limits)).toBe(false);
    expect(validOptionTitle("a", limits)).toBe(true);
    expect(validOptionTitle("å".repeat(20), limits)).toBe(true);
    expect(validOptionTitle("å".repeat(21), limits)).toBe(false);
    expect(validOptionTitle(`  ${"a".repeat(20)}  `, limits)).toBe(true);
    expect(characters("👍👍")).toBe(2);
  });

  it("names what stops a draft: a title out of bounds, or too many options", () => {
    expect(optionsProblem([], limits)).toBeNull();
    expect(optionsProblem([{ id: "a", title: "Yes" }], limits)).toBeNull();
    expect(optionsProblem([{ id: "a", title: "" }], limits)).toBe("title");
    expect(optionsProblem([{ id: "a", title: "x".repeat(21) }], limits)).toBe("title");
    const many = Array.from({ length: 14 }, (_, i) => ({ id: `o${i}`, title: "Ok" }));
    expect(optionsProblem(many, limits)).toBe("count");
  });

  it("finds a free id", () => {
    expect(uniqueOptionId([], "option")).toBe("option");
    expect(uniqueOptionId([{ id: "option", title: "" }], "option")).toBe("option-2");
  });
});

describe("a Messenger conversation's limits and window", () => {
  it("is a chat by its channel's type only", () => {
    expect(isChat({ channelType: "messenger" })).toBe(true);
    expect(isChat({ channelType: "email" })).toBe(false);
    expect(isChat({})).toBe(false);
  });

  it("works from the provider's limits, and Messenger's own when it answers none", () => {
    expect(limitsOf({ componentLimits: null })).toEqual(defaultComponentLimits);
    expect(limitsOf({ componentLimits: { ...limits, maxAnswerOptions: 3 } }).maxAnswerOptions).toBe(3);
  });

  it("lets a reply be written while the window is open or extended, and not once it is closed", () => {
    expect(canReplyInWindow({ state: "open", closesAt: "2026-01-02T00:00:00Z" })).toBe(true);
    expect(canReplyInWindow({ state: "human_agent", closesAt: "2026-01-08T00:00:00Z" })).toBe(true);
    expect(canReplyInWindow({ state: "closed", closesAt: "2026-01-02T00:00:00Z" })).toBe(false);
    expect(canReplyInWindow(null)).toBe(true);
  });

  it("holds the text to the provider's limit", () => {
    expect(textWithinLimit("a".repeat(2000), limits)).toBe(true);
    expect(textWithinLimit("a".repeat(2001), limits)).toBe(false);
  });
});

describe("the request for an e-mail address among the quick replies", () => {
  it("takes one of the 13 slots", () => {
    let options = addOption([], limits, 1);
    for (let i = 1; i < 12; i++) options = addOption(options, limits, 1);
    expect(options).toHaveLength(12);
    expect(addOption(options, limits, 1)).toHaveLength(12);
    expect(addYesNo(options.slice(0, 11), yesNo, limits, 1)).toHaveLength(11);
    const titled = options.map((option) => ({ ...option, title: "Ok" }));
    expect(optionsProblem(titled, limits, 1)).toBeNull();
    expect(optionsProblem([...titled, { id: "x", title: "Ok" }], limits, 1)).toBe("count");
  });
});
