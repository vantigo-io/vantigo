import { z } from "zod";

/** Empty strings from form inputs become null (fields are optional). */
const emptyToNull = (value: unknown) =>
  typeof value === "string" && value.trim() === "" ? null : value;

export const contactBaseSchema = z.object({
  name: z.string().trim().min(1).max(200),
  email: z.preprocess(emptyToNull, z.email().max(254).nullish()),
  phone: z.preprocess(emptyToNull, z.string().trim().min(4).max(20).nullish()),
  notes: z.string().trim().max(5000).nullish(),
});

export const contactCreateSchema = contactBaseSchema;
export const contactUpdateSchema = contactBaseSchema.partial();

/** Relationship-specific fields living on the customer<->contact link. */
export const contactLinkFieldsSchema = z.object({
  role: z.preprocess(emptyToNull, z.string().trim().max(200).nullish()),
  isPrimary: z.boolean().default(false),
});

/** POST /api/customers/[id]/contacts: link an existing contact or create + link. */
export const customerContactCreateSchema = z.union([
  contactLinkFieldsSchema.extend({ contactId: z.number().int().positive() }),
  contactLinkFieldsSchema.extend({ contact: contactCreateSchema }),
]);

export const customerContactUpdateSchema = contactLinkFieldsSchema.partial();

export const contactSortFields = ["name", "email", "createdAt"] as const;

export const contactListQuerySchema = z.object({
  page: z.coerce.number().int().min(1).default(1),
  pageSize: z.coerce.number().int().min(1).max(100).default(25),
  sortBy: z.enum(contactSortFields).default("name"),
  sortDir: z.enum(["asc", "desc"]).default("asc"),
  search: z.string().trim().max(200).optional(),
});

export type ContactCreateInput = z.infer<typeof contactCreateSchema>;
export type ContactUpdateInput = z.infer<typeof contactUpdateSchema>;
export type CustomerContactCreateInput = z.infer<typeof customerContactCreateSchema>;
export type CustomerContactUpdateInput = z.infer<typeof customerContactUpdateSchema>;
export type ContactListQuery = z.infer<typeof contactListQuerySchema>;
