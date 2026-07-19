import { relations } from "drizzle-orm";
import { boolean, index, integer, text, timestamp } from "drizzle-orm/pg-core";
import { schema } from "./schema";
import { users } from "./users";

export const twoFactors = schema.table(
  "two_factors",
  {
    id: text("id").primaryKey(),
    secret: text("secret").notNull(),
    backupCodes: text("backup_codes").notNull(),
    userId: text("user_id")
      .notNull()
      .references(() => users.id, { onDelete: "cascade" }),
    verified: boolean("verified").default(true),
    failedVerificationCount: integer("failed_verification_count").default(0),
    lockedUntil: timestamp("locked_until"),
  },
  (table) => [
    index("two_factors_secret_idx").on(table.secret),
    index("two_factors_userId_idx").on(table.userId),
  ],
);

export const twoFactorsRelations = relations(twoFactors, ({ one }) => ({
  user: one(users, {
    fields: [twoFactors.userId],
    references: [users.id],
  }),
}));
