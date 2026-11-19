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
