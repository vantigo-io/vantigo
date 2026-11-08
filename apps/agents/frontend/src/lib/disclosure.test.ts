import { describe, expect, it } from "vitest";
import { disclosureLine, previewLanguages } from "./disclosure";

describe("the disclosure line", () => {
  it("words the three Norwegian forms", () => {
    expect(disclosureLine("assistant", "nb", "Hanna", "Hansen Rør")).toBe(
      "«Hanna» er «Hansen Rør» sin virtuelle assistent (KI).",
    );
    expect(disclosureLine("talking", "nb", "Hanna", "Hansen Rør")).toBe(
      "Du snakker med «Hanna», en KI-assistent hos «Hansen Rør».",
    );
    expect(disclosureLine("compact", "nb", "Hanna", "Hansen Rør")).toBe(
      "«Hanna» (KI) · virtuell assistent, «Hansen Rør».",
    );
  });

  it("words the three English forms, compact staying Norwegian-first", () => {
    expect(disclosureLine("assistant", "en", "Hanna", "Hansen Rør")).toBe(
      "«Hanna» is «Hansen Rør»'s virtual assistant (AI).",
    );
    expect(disclosureLine("talking", "en", "Hanna", "Hansen Rør")).toBe(
      "You are talking to «Hanna», an AI assistant at «Hansen Rør».",
    );
    expect(disclosureLine("compact", "en", "Hanna", "Hansen Rør")).toBe(
      disclosureLine("compact", "nb", "Hanna", "Hansen Rør"),
    );
  });

  it("previews both languages when the agent follows the customer", () => {
    expect(previewLanguages("customer")).toEqual(["nb", "en"]);
    expect(previewLanguages("nb")).toEqual(["nb"]);
    expect(previewLanguages("en")).toEqual(["en"]);
  });
});
