import type { ComponentLimits, MessageCard } from "../api/conversations";
import {
  communicationsImages,
  type PickerImage,
  type PickerPrice,
  type PickerProduct,
  pickerPriceOf,
  pickerThumbnailUrl,
} from "../api/products";
import { characters } from "./chat";

/** The id of the Buy choice (Messenger design N16): what a "Wants to buy" answer is recognised by. */
export const BUY_CHOICE_ID = "buy";
/** The id of the optional second choice the agent writes. */
export const SECOND_CHOICE_ID = "second";

/** One card of a product being attached: the image, the title and the subtitle the agent may edit. */
export type DraftCard = {
  key: string;
  productId: number;
  imageId: number;
  thumbnailUrl: string;
  title: string;
  subtitle: string;
};

/** A product being attached to a message: its cards, and the choices every card carries. */
export type ProductDraft = {
  productId: number;
  productName: string;
  cards: DraftCard[];
  /** Kjøp or Buy, by the language. */
  buyTitle: string;
  /** The agent's own second choice, "" for none. */
  secondTitle: string;
};

/** The title of the Buy choice by the language the message is written in: Kjøp in Norwegian, Buy otherwise. */
export const buyTitleFor = (locale: string): string => (locale.toLowerCase().startsWith("nb") ? "Kjøp" : "Buy");

/**
 * A card's subtitle prefilled with the price, as the product page shows it — excluding VAT —, cut to the
 * limit. The agent can edit or clear it; the server treats it as text and derives no price (N16).
 */
export const priceSubtitle = (
  price: PickerPrice | null,
  format: (amount: number, currency: string) => string,
  exVat: string,
  limits: Pick<ComponentLimits, "maxCardSubtitle">,
): string => {
  if (!price) return "";
  return [...`${format(price.amount, price.currency)} ${exVat}`].slice(0, limits.maxCardSubtitle).join("");
};

/**
 * The cards of a product: one per image marked for Communications, in the images' order, at most the
 * provider's limit. Empty when the product has no such image — it cannot be attached.
 */
export const newProductDraft = (
  product: PickerProduct,
  images: readonly PickerImage[],
  options: {
    limits: ComponentLimits;
    locale: string;
    pricing: boolean;
    format: (amount: number, currency: string) => string;
    exVat: string;
  },
): ProductDraft => {
  const title = [...product.name.trim()].slice(0, options.limits.maxCardTitle).join("");
  const subtitle = options.pricing
    ? priceSubtitle(pickerPriceOf(product), options.format, options.exVat, options.limits)
    : "";
  return {
    productId: product.id,
    productName: product.name,
    buyTitle: buyTitleFor(options.locale),
    secondTitle: "",
    cards: communicationsImages(images)
      .slice(0, options.limits.maxCards)
      .map((image) => ({
        key: `image-${image.id}`,
        productId: product.id,
        imageId: image.id,
        thumbnailUrl: pickerThumbnailUrl(product.id, image.id),
        title,
        subtitle,
      })),
  };
};

/** What stops a draft from being sent, as a code, or null. */
export const draftProblem = (draft: ProductDraft, limits: ComponentLimits): "subtitle" | "choice" | "none" | null => {
  if (draft.cards.length === 0) return "none";
  if (draft.cards.some((card) => characters(card.subtitle) > limits.maxCardSubtitle)) return "subtitle";
  const bad = (title: string) => characters(title.trim()) < 1 || characters(title.trim()) > limits.maxChoiceTitle;
  if (bad(draft.buyTitle) || (draft.secondTitle !== "" && bad(draft.secondTitle))) return "choice";
  return null;
};

/** The draft as the cards of a reply: each names its product image, and carries Buy and the second choice. */
export const draftCards = (draft: ProductDraft): MessageCard[] =>
  draft.cards.map((card) => ({
    title: card.title,
    ...(card.subtitle.trim() ? { subtitle: card.subtitle.trim() } : {}),
    productImageId: card.imageId,
    choices: [
      { id: BUY_CHOICE_ID, title: draft.buyTitle.trim() },
      ...(draft.secondTitle.trim() ? [{ id: SECOND_CHOICE_ID, title: draft.secondTitle.trim() }] : []),
    ],
  }));
