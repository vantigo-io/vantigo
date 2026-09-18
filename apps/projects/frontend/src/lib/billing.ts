/** How a project is billed, in the order the form offers them. */
export const billingTypes = ["time-and-materials", "fixed-price", "non-billable"] as const;

export type BillingType = (typeof billingTypes)[number];

/** What a billing line is priced by (design §5, D9). */
export const pricingModes = ["list", "fixed", "discount"] as const;

export type PricingMode = (typeof pricingModes)[number];

const billingTypeKeys: Record<BillingType, string> = {
  "time-and-materials": "billingTypeTimeAndMaterials",
  "fixed-price": "billingTypeFixedPrice",
  "non-billable": "billingTypeNonBillable",
};

const pricingModeKeys: Record<PricingMode, string> = {
  list: "pricingModeList",
  fixed: "pricingModeFixed",
  discount: "pricingModeDiscount",
};

/** The `projects` catalog key naming this billing type. */
export const billingTypeLabelKey = (billingType: BillingType): string => billingTypeKeys[billingType];

/** The `projects` catalog key naming this pricing rule. */
export const pricingModeLabelKey = (mode: PricingMode): string => pricingModeKeys[mode];

export const isBillingType = (value: string): value is BillingType =>
  (billingTypes as readonly string[]).includes(value);

export const isPricingMode = (value: string): value is PricingMode =>
  (pricingModes as readonly string[]).includes(value);
