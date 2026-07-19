import { relations } from "drizzle-orm";
import { boolean, text, timestamp } from "drizzle-orm/pg-core";
import { accounts } from "./accounts";
import { schema } from "./schema";
import { sessions } from "./sessions";

export const users = schema.table("users", {
  id: text("id").primaryKey(),
  name: text("name").notNull(),
  email: text("email").notNull().unique(),
  emailVerified: boolean("email_verified").default(false).notNull(),
  image: text("image"),
  language: text("language"),
  // "business" | "private" | "last" — default legal type when creating customers.
  preferredCustomerType: text("preferred_customer_type"),
  // Most recently used legal type; backs the "last" preference.
  lastCustomerType: text("last_customer_type"),
  twoFactorEnabled: boolean("two_factor_enabled").default(false),
  createdAt: timestamp("created_at").defaultNow().notNull(),
  updatedAt: timestamp("updated_at")
    .defaultNow()
    .$onUpdate(() => /* @__PURE__ */ new Date())
    .notNull(),
});

export const usersRelations = relations(users, ({ many }) => ({
  sessions: many(sessions),
  accounts: many(accounts),
}));
