import { auth } from "@vantigo/customers/lib/auth";
import {
  isLocale,
  LOCALE_COOKIE,
  type Locale,
  negotiateLocale,
} from "@vantigo/customers/lib/i18n/locales";
import { cookies, headers } from "next/headers";
import { getRequestConfig } from "next-intl/server";

async function resolveLocale(): Promise<Locale> {
  const requestHeaders = await headers();

  // 1. Authenticated user's saved language
  const session = await auth.api.getSession({ headers: requestHeaders });
  const userLanguage = session?.user?.language;
  if (isLocale(userLanguage)) return userLanguage;

  // 2. Locale cookie (logged-out visitors, pre-login pages)
  const cookieLocale = (await cookies()).get(LOCALE_COOKIE)?.value;
  if (isLocale(cookieLocale)) return cookieLocale;

  // 3. Accept-Language negotiation
  return negotiateLocale(requestHeaders.get("accept-language"));
}

export default getRequestConfig(async () => {
  const locale = await resolveLocale();

  return {
    locale,
    messages: (await import(`../../messages/${locale}.json`)).default,
  };
});
