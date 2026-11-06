import type { ComponentLimits, ConversationDetail, MessageChoice, ReplyWindow } from "../api/conversations";

/**
 * What a Messenger conversation's composer and thread work from (Messenger design N4, N8): the limits
 * the server answers for the channel's provider, and these when it answers none — Messenger's own.
 */
export const defaultComponentLimits: ComponentLimits = {
  maxText: 2000,
  maxAnswerOptions: 13,
  maxAnswerTitle: 20,
  shareEmail: true,
  maxCards: 10,
  maxCardChoices: 3,
  maxCardTitle: 80,
  maxCardSubtitle: 80,
  maxChoiceTitle: 20,
};

/** Whether a conversation is a Messenger chat: its thread is bubbles and its composer a chat composer. */
export const isChat = (conversation: Pick<ConversationDetail, "channelType">): boolean =>
  conversation.channelType === "messenger";

export const limitsOf = (conversation: Pick<ConversationDetail, "componentLimits">): ComponentLimits =>
  conversation.componentLimits ?? defaultComponentLimits;

/** A chat composer's answer option: an id unique within the message, and the title being written. */
export type DraftOption = MessageChoice;

/** The characters in a string, as the platform counts them: one for a letter whatever its bytes. */
export const characters = (text: string): number => [...text].length;

/** The id for a new option: `base` when it is free, else `base-2`, `base-3`, … — [a-z0-9-], at most 40 characters. */
export const uniqueOptionId = (options: DraftOption[], base: string): string => {
  const taken = new Set(options.map((option) => option.id));
  if (!taken.has(base)) return base;
  for (let n = 2; ; n++) {
    const id = `${base}-${n}`;
    if (!taken.has(id)) return id;
  }
};

/** *Add Yes / No*: the two options, titled in the user's language, ids kept unique; nothing when they do not fit. */
export const addYesNo = (
  options: DraftOption[],
  titles: { yes: string; no: string },
  limits: ComponentLimits,
  reserved = 0,
): DraftOption[] => {
  if (options.length + reserved + 2 > limits.maxAnswerOptions) return options;
  const yes = { id: uniqueOptionId(options, "yes"), title: titles.yes };
  const withYes = [...options, yes];
  return [...withYes, { id: uniqueOptionId(withYes, "no"), title: titles.no }];
};

/**
 * *Add option*: one empty option, up to the provider's limit. `reserved` is the quick replies already
 * taken beside the options — one when *Ask for e-mail* is on, since it is a quick reply too.
 */
export const addOption = (options: DraftOption[], limits: ComponentLimits, reserved = 0): DraftOption[] => {
  if (options.length + reserved >= limits.maxAnswerOptions) return options;
  return [...options, { id: uniqueOptionId(options, "option"), title: "" }];
};

/** An option's title the platform will take: 1 to the limit's characters once trimmed. */
export const validOptionTitle = (title: string, limits: ComponentLimits): boolean => {
  const n = characters(title.trim());
  return n >= 1 && n <= limits.maxAnswerTitle;
};

/** What stops a draft from being sent, as a code: a title out of bounds, or more options than the limit. */
export const optionsProblem = (
  options: DraftOption[],
  limits: ComponentLimits,
  reserved = 0,
): "title" | "count" | null => {
  if (options.length + reserved > limits.maxAnswerOptions) return "count";
  return options.some((option) => !validOptionTitle(option.title, limits)) ? "title" : null;
};

/**
 * Whether a reply can be written in a conversation now: not when the window is closed — the customer must
 * write again first (design N6). A conversation that is not a chat has no window.
 */
export const canReplyInWindow = (window: ReplyWindow | null | undefined): boolean => window?.state !== "closed";

/** Whether the text is within the provider's limit, counted in characters. */
export const textWithinLimit = (text: string, limits: ComponentLimits): boolean => characters(text) <= limits.maxText;
