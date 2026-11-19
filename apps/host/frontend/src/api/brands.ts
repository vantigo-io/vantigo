import type { components } from "../api-schema";
import { request } from "./request";

/** A brand as the admin sees it (brands design B33): its published version, its pending draft, the checks of the one being edited. */
export type Brand = components["schemas"]["BrandResponse"];
export type BrandVersion = components["schemas"]["BrandVersion"];
export type BrandSummary = components["schemas"]["BrandSummary"];
export type BrandCheck = components["schemas"]["BrandCheck"];
export type BrandSupport = components["schemas"]["BrandSupport"];
export type BrandInputs = Pick<components["schemas"]["BrandUpdateRequest"], "name" | "title" | "primary" | "radius"> & {
  support?: BrandSupport;
};
export type LogoSlot = "mark_light" | "mark_dark" | "icon" | "wordmark";

const base = "/api/v1/identity/brands";
const json = { "Content-Type": "application/json" };

export const brandsQueryKey = ["brands"] as const;
export const brandQueryKey = (id: string) => ["brands", id] as const;

export const listBrands = () => request<{ items: BrandSummary[] }>(base);

export const fetchBrand = (id: string) => request<Brand>(`${base}/${id}`);

/** Saves the draft from the inputs, against the revision the brand was read at; a stale one is a 409 stale_revision. */
export const saveBrandDraft = (id: string, revision: number, body: BrandInputs) =>
  request<Brand>(`${base}/${id}`, {
    method: "PUT",
    headers: { ...json, "If-Match": String(revision) },
    body: JSON.stringify(body),
  });

/** Publishes the draft; the server refuses 422 brand_contrast while a colour pair is below AA. */
export const publishBrand = (id: string, revision: number) =>
  request<Brand>(`${base}/${id}/publish`, { method: "POST", headers: { "If-Match": String(revision) } });

export const discardBrandDraft = (id: string, revision: number) =>
  request<Brand>(`${base}/${id}/discard-draft`, { method: "POST", headers: { "If-Match": String(revision) } });

/** Uploads a logo into the draft; the server judges the file (PNG or JPEG, 512 KiB, 16 to 2,000 px). */
export const uploadBrandLogo = (id: string, slot: LogoSlot, file: File) => {
  const form = new FormData();
  form.append("file", file);
  return request<Brand>(`${base}/${id}/logos/${slot}`, { method: "PUT", body: form });
};

export const removeBrandLogo = (id: string, slot: LogoSlot) =>
  request<Brand>(`${base}/${id}/logos/${slot}`, { method: "DELETE" });

/** Where the draft's logo can be shown from, for holders of identity:brands; the hash busts the cache. */
export const draftLogoPath = (id: string, slot: LogoSlot, sha256: string) =>
  `${base}/${id}/logos/${slot}?draft=true&v=${sha256.slice(0, 12)}`;

// --- Several brands, rules, and a person's own choice (brands design B-B) ---

type Schemas = components["schemas"];
export type BrandRule = Schemas["BrandRule"];
export type BrandRulesResponse = Schemas["BrandRulesResponse"];
export type BrandRuleInput = Schemas["BrandRuleInput"];
export type BrandRuleKind = BrandRuleInput["kind"];
export type BrandRuleTargets = Schemas["BrandRuleTargetsResponse"];
export type BrandResolution = Schemas["BrandResolution"];

export const brandRulesQueryKey = ["brand-rules"] as const;
export const brandRuleTargetsQueryKey = ["brand-rule-targets"] as const;

const lifecycle = (id: string, action: string, revision: number) =>
  request<Brand>(`${base}/${id}/${action}`, { method: "POST", headers: { "If-Match": String(revision) } });

/** Creates a brand as a draft from its inputs; 409 brand_name_taken when another brand has the name. */
export const createBrand = (body: BrandInputs) =>
  request<Brand>(base, { method: "POST", headers: json, body: JSON.stringify(body) });
export const duplicateBrand = (id: string, revision: number) => lifecycle(id, "duplicate", revision);
export const archiveBrand = (id: string, revision: number) => lifecycle(id, "archive", revision);
export const restoreBrand = (id: string, revision: number) => lifecycle(id, "restore", revision);
export const makeDefaultBrand = (id: string, revision: number) => lifecycle(id, "make-default", revision);
/** Deletes a brand that was never published; a published one is archived instead. */
export const deleteBrand = (id: string, revision: number) =>
  request<void>(`${base}/${id}`, { method: "DELETE", headers: { "If-Match": String(revision) } });

export const getBrandRules = () => request<BrandRulesResponse>("/api/v1/identity/brand-rules");

/** Replaces the whole ordered list at the revision it was read at. */
export const saveBrandRules = (revision: number, items: BrandRuleInput[]) =>
  request<BrandRulesResponse>("/api/v1/identity/brand-rules", {
    method: "PUT",
    headers: { ...json, "If-Match": String(revision) },
    body: JSON.stringify({ items }),
  });

export const getBrandRuleTargets = () => request<BrandRuleTargets>("/api/v1/identity/brand-rule-targets");

/** Who sees which brand, for a person named by e-mail address. */
export const lookupBrandResolution = (email: string) =>
  request<BrandResolution>(`${base}/resolution?email=${encodeURIComponent(email)}`);

/** The person's own choice among their candidate brands; null is automatic. 409 brand_not_candidate otherwise. */
export const chooseMyBrand = (brandId: string | null) =>
  request<unknown>("/api/v1/identity/account/brand", {
    method: "PUT",
    headers: json,
    body: JSON.stringify({ brandId }),
  });

/** A brand's inputs as a JSON file moves them between a test and a production instance (B21): never logos or tokens. */
export const exportInputsOf = (brand: Brand): BrandInputs => {
  const v = brand.draft ?? brand.published;
  return {
    name: v?.name ?? "",
    title: v?.title ?? "",
    primary: v?.primary ?? "",
    radius: v?.radius ?? "md",
    support: v?.support ?? {},
  };
};
