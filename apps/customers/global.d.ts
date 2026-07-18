import type { Locale } from "@vantigo/customers/lib/i18n/locales";
import type messages from "./messages/en.json";

declare module "next-intl" {
  interface AppConfig {
    Locale: Locale;
    Messages: typeof messages;
  }
}
