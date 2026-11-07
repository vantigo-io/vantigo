/** A callout's kinds (knowledge design K18), in the switcher's order. */
export const calloutKinds = ["info", "tip", "warning", "danger"] as const;
export type CalloutKind = (typeof calloutKinds)[number];

export const isCalloutKind = (value: unknown): value is CalloutKind => calloutKinds.includes(value as CalloutKind);

/** The emoji the switcher offers; a callout without one shows its kind's icon. */
export const calloutEmojis = ["💡", "📌", "✅", "⚠️", "❗", "🔥", "📎", "🗓️", "🔒", "👉", "🧭", "📣"] as const;
