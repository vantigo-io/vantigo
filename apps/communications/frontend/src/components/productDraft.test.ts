import { describe, expect, it } from "vitest";
import { defaultComponentLimits } from "./chat";
import { buyTitleFor, draftCards, draftProblem, newProductDraft, priceSubtitle } from "./productDraft";

const format = (amount: number, currency: string) => `${currency} ${amount}`;
const product = (overrides = {}) => ({
  id: 7,
  name: "Chair",
  sku: "CH-1",
  status: "Active",
  effectivePrices: [{ amount: 1200, currency: "NOK" }],
  variants: [],
  ...overrides,
});
const image = (id: number, order: number, uses: string[]) => ({
  id,
  productId: 7,
  isPrimary: order === 0,
  sortOrder: order,
  uses,
});
const options = { limits: defaultComponentLimits, locale: "en", pricing: true, format, exVat: "ex VAT" };

describe("a product draft", () => {
  it("titles Buy by the language: Kjøp in Norwegian, Buy otherwise", () => {
    expect(buyTitleFor("nb")).toBe("Kjøp");
    expect(buyTitleFor("en")).toBe("Buy");
  });

  it("is a card per image marked for Communications, in the images' order", () => {
    const draft = newProductDraft(
      product(),
      [image(3, 2, ["communications"]), image(1, 0, ["communications", "marketing"]), image(2, 1, ["marketing"])],
      options,
    );
    expect(draft.cards.map((card) => card.imageId)).toEqual([1, 3]);
    expect(draft.cards.every((card) => card.title === "Chair")).toBe(true);
  });

  it("has no card for a product with no image marked, and cannot be sent", () => {
    const draft = newProductDraft(product(), [image(1, 0, ["marketing"])], options);
    expect(draft.cards).toEqual([]);
    expect(draftProblem(draft, defaultComponentLimits)).toBe("none");
  });

  it("takes at most the provider's cards", () => {
    const many = Array.from({ length: 14 }, (_, i) => image(i + 1, i, ["communications"]));
    expect(newProductDraft(product(), many, options).cards).toHaveLength(10);
  });

  it("prefills the subtitle with the price, ex VAT, only under pricing", () => {
    const images = [image(1, 0, ["communications"])];
    expect(newProductDraft(product(), images, options).cards[0]?.subtitle).toBe("NOK 1200 ex VAT");
    expect(newProductDraft(product(), images, { ...options, pricing: false }).cards[0]?.subtitle).toBe("");
    expect(newProductDraft(product({ effectivePrices: [] }), images, options).cards[0]?.subtitle).toBe("");
  });

  it("reads the price from the first variant that has one when the product has none", () => {
    const draft = newProductDraft(
      product({
        effectivePrices: [],
        variants: [{ effectivePrices: [] }, { effectivePrices: [{ amount: 90, currency: "NOK" }] }],
      }),
      [image(1, 0, ["communications"])],
      options,
    );
    expect(draft.cards[0]?.subtitle).toBe("NOK 90 ex VAT");
  });

  it("cuts a price to the subtitle limit", () => {
    expect(
      priceSubtitle({ amount: 1, currency: "NOK" }, () => "x".repeat(100), "ex VAT", { maxCardSubtitle: 80 }),
    ).toHaveLength(80);
  });

  it("makes the cards of a reply: Buy first, the second choice when written, an empty subtitle left out", () => {
    const draft = newProductDraft(product(), [image(1, 0, ["communications"])], options);
    const [card] = draft.cards;
    if (!card) throw new Error("no card");
    draft.cards = [{ ...card, subtitle: "  " }];
    expect(draftCards(draft)).toEqual([{ title: "Chair", productImageId: 1, choices: [{ id: "buy", title: "Buy" }] }]);
    draft.secondTitle = " More ";
    expect(draftCards(draft)[0]?.choices).toEqual([
      { id: "buy", title: "Buy" },
      { id: "second", title: "More" },
    ]);
  });

  it("is held back for a subtitle or a choice title out of bounds", () => {
    const draft = newProductDraft(product(), [image(1, 0, ["communications"])], options);
    expect(draftProblem(draft, defaultComponentLimits)).toBeNull();
    expect(draftProblem({ ...draft, buyTitle: " " }, defaultComponentLimits)).toBe("choice");
    expect(draftProblem({ ...draft, secondTitle: "x".repeat(21) }, defaultComponentLimits)).toBe("choice");
    const [card] = draft.cards;
    if (!card) throw new Error("no card");
    expect(draftProblem({ ...draft, cards: [{ ...card, subtitle: "x".repeat(81) }] }, defaultComponentLimits)).toBe(
      "subtitle",
    );
  });
});
