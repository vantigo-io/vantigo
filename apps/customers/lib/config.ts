import { z } from "zod";
import "dotenv/config";

const configSchema = z.object({
  VANTIGO_CUSTOMERS_BASE_URL: z.string().default("http://localhost:10010"),
  VANTIGO_CUSTOMERS_DATABASE_URL: z.string(),

  VANTIGO_CUSTOMERS_AUTH_SECRET: z.string(),

  VANTIGO_CUSTOMERS_EMAIL_AND_PASSWORD_ENABLED: z.stringbool().default(false),

  VANTIGO_CUSTOMERS_RATE_LIMIT_ENABLED: z.stringbool().default(true),
});

const parsedConfig = configSchema.safeParse(process.env);

if (!parsedConfig.success) {
  throw new Error(
    `❌ Invalid environment configuration:\n${JSON.stringify(z.treeifyError(parsedConfig.error), null, 2)}`,
  );
}

export const config = parsedConfig.data;
export type Config = z.infer<typeof configSchema>;
