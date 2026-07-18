import { LanguageToggle } from "@vantigo/customers/lib/i18n/language-toggle";
import { getTranslations } from "next-intl/server";

export default async function Home() {
  const t = await getTranslations("home");

  return (
    <>
      <LanguageToggle />
      <h1>{t("greeting")}</h1>
    </>
  );
}
