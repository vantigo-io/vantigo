import { customerStatuses, legalTypes } from "@vantigo/customers/database/schema/customers";
import { z } from "zod";
import { validateLegalId } from "./legal-id";

const legalIdRefinement = (
  data: { legalCountry: string; legalType: (typeof legalTypes)[number]; legalId: string },
  ctx: z.RefinementCtx,
) => {
  if (!validateLegalId(data.legalCountry, data.legalType, data.legalId)) {
    ctx.addIssue({
      code: "custom",
      path: ["legalId"],
      message: "invalid_legal_id",
      params: { country: data.legalCountry, legalType: data.legalType },
    });
  }
};

/** Empty strings from form inputs become null (fields are optional). */
const emptyToNull = (value: unknown) =>
  typeof value === "string" && value.trim() === "" ? null : value;

const customerBaseSchema = z.object({
  legalName: z.string().trim().min(1).max(200),
  legalType: z.enum(legalTypes),
  legalCountry: z
    .string()
    .trim()
    .length(2)
    .transform((value) => value.toUpperCase()),
  legalId: z.string().trim().min(1).max(32),
  email: z.preprocess(emptyToNull, z.email().max(254).nullish()),
  phone: z.preprocess(emptyToNull, z.string().trim().min(4).max(20).nullish()),
  notes: z.string().trim().max(5000).nullish(),
});

export const customerCreateSchema = customerBaseSchema.superRefine(legalIdRefinement);

export const customerUpdateSchema = customerBaseSchema
  .extend({
    status: z.enum(customerStatuses),
  })
  .partial()
  .superRefine((data, ctx) => {
    // legalCountry/legalType/legalId must be re-validated together; require
    // all three when any of them changes.
    if (
      data.legalId !== undefined ||
      data.legalType !== undefined ||
      data.legalCountry !== undefined
    ) {
      if (
        data.legalId === undefined ||
        data.legalType === undefined ||
        data.legalCountry === undefined
      ) {
        ctx.addIssue({
          code: "custom",
          path: ["legalId"],
          message: "legal_fields_must_change_together",
        });
        return;
      }
      legalIdRefinement(
        { legalCountry: data.legalCountry, legalType: data.legalType, legalId: data.legalId },
        ctx,
      );
    }
  });

export const customerSortFields = [
  "id",
  "legalName",
  "legalType",
  "email",
  "status",
  "createdAt",
] as const;

export const customerListQuerySchema = z.object({
  page: z.coerce.number().int().min(1).default(1),
  pageSize: z.coerce.number().int().min(1).max(100).default(25),
  sortBy: z.enum(customerSortFields).default("id"),
  sortDir: z.enum(["asc", "desc"]).default("asc"),
  search: z.string().trim().max(200).optional(),
  legalType: z.enum(legalTypes).optional(),
  status: z.enum(customerStatuses).optional(),
  legalId: z.string().trim().max(32).optional(),
  email: z.string().trim().max(254).optional(),
});

export type CustomerCreateInput = z.infer<typeof customerCreateSchema>;
export type CustomerUpdateInput = z.infer<typeof customerUpdateSchema>;
export type CustomerListQuery = z.infer<typeof customerListQuerySchema>;
