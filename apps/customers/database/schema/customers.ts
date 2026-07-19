import { index, integer, text, timestamp } from "drizzle-orm/pg-core";
import { schema } from "./schema";

export const legalTypes = ["private", "business"] as const;
export type LegalType = (typeof legalTypes)[number];

export const customerStatuses = ["active", "archived"] as const;
export type CustomerStatus = (typeof customerStatuses)[number];

export const customers = schema.table(
  "customers",
  {
    // Customer numbers are user-facing and start at 1001.
    id: integer("id").generatedAlwaysAsIdentity({ startWith: 1001 }).primaryKey(),
    legalName: text("legal_name").notNull(),
    legalType: text("legal_type", { enum: legalTypes }).notNull(),
    /** ISO 3166-1 alpha-2 country code deciding which legal-id rules apply. */
    legalCountry: text("legal_country").notNull(),
    /** Fødselsnummer (private) or organisasjonsnummer (business) for NO. */
    legalId: text("legal_id").notNull(),
    email: text("email").notNull(),
    phone: text("phone").notNull(),
    status: text("status", { enum: customerStatuses }).default("active").notNull(),
    notes: text("notes"),
    createdAt: timestamp("created_at").defaultNow().notNull(),
    updatedAt: timestamp("updated_at")
      .defaultNow()
      .$onUpdate(() => /* @__PURE__ */ new Date())
      .notNull(),
  },
  (table) => [
    index("customers_legal_name_idx").on(table.legalName),
    index("customers_status_idx").on(table.status),
    index("customers_legal_type_idx").on(table.legalType),
  ],
);

export type Customer = typeof customers.$inferSelect;
export type NewCustomer = typeof customers.$inferInsert;
