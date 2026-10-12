import type { StaffDocumentCategory } from "../api/documents";
import type { StaffPerson } from "../api/people";
import type { ApiError } from "../api/request";
import { fieldRefusals } from "./errors";

type Translate = (key: string, options?: Record<string, unknown>) => string;

/**
 * The four kinds the server keeps (design S14) — PDF, JPEG, PNG and HEIC —
 * by every type and extension it takes as naming one of them (`objects.go`'s
 * documentTypes), HEIF and its sequences among HEIC's.
 */
const DOCUMENT_TYPES = [
  "application/pdf",
  "application/x-pdf",
  "image/jpeg",
  "image/jpg",
  "image/pjpeg",
  "image/png",
  "image/heic",
  "image/heif",
  "image/heic-sequence",
  "image/heif-sequence",
];
const DOCUMENT_EXTENSIONS = ["pdf", "jpg", "jpeg", "jpe", "png", "heic", "heif"];
export const DOCUMENT_ACCEPT = [...DOCUMENT_TYPES, ...DOCUMENT_EXTENSIONS.map((extension) => `.${extension}`)].join(
  ",",
);
export const DOCUMENT_MAX_BYTES = 10 * 1024 * 1024;

/**
 * What the page can tell before sending: too large, or named as something
 * else — a type or an extension outside the four (`text/plain`, `.docx`).
 * A type that names nothing (none, `application/octet-stream`) and a name
 * without an extension are no evidence either way, as on the server, whose
 * reading of the bytes has the last word.
 */
export const fileRefusal = (file: File, t: Translate): string | null => {
  if (file.size > DOCUMENT_MAX_BYTES) return t("documentTooLarge", { name: file.name });
  const type = file.type.toLowerCase();
  const namesType = type !== "" && type !== "application/octet-stream";
  const extension = file.name.includes(".") ? file.name.split(".").pop()?.toLowerCase() : undefined;
  if ((namesType && !DOCUMENT_TYPES.includes(type)) || (extension && !DOCUMENT_EXTENSIONS.includes(extension)))
    return t("documentWrongType", { name: file.name });
  return null;
};

/**
 * The categories a document may be filed under by this reader: those in use
 * and, when they may see health documents (`canSeeHealth`), the restricted
 * ones — the server refuses the rest as `category_restricted`, and a choice
 * the reader cannot make is not offered.
 */
export const fileableCategories = (categories: StaffDocumentCategory[], canSeeHealth: boolean) =>
  categories.filter((category) => !category.archivedAt && (canSeeHealth || !category.restricted));

/**
 * Where a refusal of a document's write belongs: `category_restricted` on the
 * category, in the reader's words; the 200 cap, an unreachable store and the
 * upload allowance as a banner; a field error on the field it names (the
 * file's type and size on the file), anything else in the banner too.
 */
export const placeDocumentRefusal = (
  error: Error,
  t: Translate,
  setErrors: (errors: Record<string, string>) => void,
  setBanner: (message: string) => void,
) => {
  const { status, code } = error as ApiError;
  if (code === "category_restricted") return setErrors({ categoryId: t("documentRestricted") });
  if (code === "document_count_exceeded") return setBanner(t("documentCountExceeded"));
  if (code === "storage_unavailable" || status === 503) return setBanner(t("documentStoreDown"));
  if (status === 429) return setBanner(t("documentUploadsLimited"));
  const { fields, rest } = fieldRefusals(error, ["file", "categoryId", "validTo", "signedAt"]);
  setErrors(fields);
  if (rest.length > 0) setBanner(rest.join(" "));
};

/**
 * Whether the reader reads the person's documents (design S14): the person
 * themselves and `staff:personal-data` (`canSeeHealth` on the person), and
 * `staff:manage` (`canEdit`) — everyone else gets the archive's 404, so the
 * tab is not drawn for them.
 */
export const readsDocuments = (person: StaffPerson) => person.capabilities.canEdit || person.capabilities.canSeeHealth;
