import { describe, expect, it } from "vitest";
import { type Mention, mentionsIn, mentionText } from "./mentions";

const hanna: Mention = { userId: "u-hanna", displayName: "Hanna" };
const kari: Mention = { userId: "u-kari", displayName: "Kari Nordmann" };

describe("the mentions a note holds", () => {
  it("writes a mention as an @ and the name", () => {
    expect(mentionText("Hanna")).toBe("@Hanna");
  });

  it("keeps the picked users whose @Name is still in the text, in the order they were picked", () => {
    expect(mentionsIn("@Kari Nordmann and @Hanna, please look", [hanna, kari])).toEqual(["u-hanna", "u-kari"]);
  });

  it("drops a user whose name was deleted from the note", () => {
    expect(mentionsIn("@Hanna, please look", [hanna, kari])).toEqual(["u-hanna"]);
    expect(mentionsIn("Please look", [hanna])).toEqual([]);
  });

  it("counts a user once however often they are picked or named", () => {
    expect(mentionsIn("@Hanna @Hanna", [hanna, hanna])).toEqual(["u-hanna"]);
  });

  it("needs the name to stand alone: a longer name or a word around it is no mention", () => {
    expect(mentionsIn("@Hannah, please look", [hanna])).toEqual([]);
    expect(mentionsIn("mail@Hanna", [hanna])).toEqual([]);
    expect(mentionsIn("(@Hanna) and @Hanna.", [hanna])).toEqual(["u-hanna"]);
  });

  it("treats a name with regular-expression characters as text", () => {
    const odd: Mention = { userId: "u-odd", displayName: "A.B (Sales)" };
    expect(mentionsIn("@A.B (Sales) please", [odd])).toEqual(["u-odd"]);
    expect(mentionsIn("@AxB (Sales) please", [odd])).toEqual([]);
  });
});
