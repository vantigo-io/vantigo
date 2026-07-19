import { relations, sql } from "drizzle-orm";
import {
  boolean,
  index,
  integer,
  primaryKey,
  text,
  timestamp,
  uniqueIndex,
} from "drizzle-orm/pg-core";
import { contacts } from "./contacts";
import { customers } from "./customers";
import { schema } from "./schema";

/** Customer <-> contact link; role and primary flag are relationship-specific. */
export const customersContacts = schema.table(
  "customers_contacts",
  {
    customerId: integer("customer_id")
      .notNull()
      .references(() => customers.id, { onDelete: "cascade" }),
    contactId: integer("contact_id")
      .notNull()
      .references(() => contacts.id, { onDelete: "cascade" }),
    role: text("role"),
    isPrimary: boolean("is_primary").default(false).notNull(),
    createdAt: timestamp("created_at").defaultNow().notNull(),
  },
  (table) => [
    primaryKey({ columns: [table.customerId, table.contactId] }),
    // At most one primary contact per customer, enforced by Postgres.
    uniqueIndex("customers_contacts_primary_idx")
      .on(table.customerId)
      .where(sql`${table.isPrimary}`),
    index("customers_contacts_contact_idx").on(table.contactId),
  ],
);

export const customersContactsRelations = relations(customersContacts, ({ one }) => ({
  customer: one(customers, {
    fields: [customersContacts.customerId],
    references: [customers.id],
  }),
  contact: one(contacts, {
    fields: [customersContacts.contactId],
    references: [contacts.id],
  }),
}));

export type CustomerContact = typeof customersContacts.$inferSelect;
