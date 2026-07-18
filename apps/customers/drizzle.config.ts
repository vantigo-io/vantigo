import "dotenv/config";
import { defineConfig } from "drizzle-kit";

export default defineConfig({
  out: "./database/migrations",
  schema: "./database/schema",
  dialect: "postgresql",
  dbCredentials: {
    // biome-ignore lint/style/noNonNullAssertion: Handled by mise
    url: process.env.VANTIGO_CUSTOMERS_DATABASE_URL!,
  },
  migrations: {
    schema: "customers",
    table: "migrations",
  },
});
