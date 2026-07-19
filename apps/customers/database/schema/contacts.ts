import { relations } from "drizzle-orm";
import { index, integer, text, timestamp } from "drizzle-orm/pg-core";
import { customersContacts } from "./customers-contacts";
import { schema } from "./schema";

/** A contact person; an independent entity that can be linked to many customers. */
export const contacts = schema.table(
  "contacts",
  {
    id: integer("id").generatedAlwaysAsIdentity().primaryKey(),
    name: text("name").notNull(),
    email: text("email"),
    phone: text("phone"),
    notes: text("notes"),
    createdAt: timestamp("created_at").defaultNow().notNull(),
    updatedAt: timestamp("updated_at")
      .defaultNow()
      .$onUpdate(() => /* @__PURE__ */ new Date())
      .notNull(),
  },
  (table) => [
    index("contacts_name_idx").on(table.name),
    index("contacts_email_idx").on(table.email),
  ],
);

export const contactsRelations = relations(contacts, ({ many }) => ({
  customerLinks: many(customersContacts),
}));

export type Contact = typeof contacts.$inferSelect;
