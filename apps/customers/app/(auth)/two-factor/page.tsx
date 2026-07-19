import { getTranslations } from "next-intl/server";
import { AuthTitle } from "../auth-title";
import { TwoFactorVerifyForm } from "./two-factor-verify-form";

export default async function TwoFactorPage() {
  const t = await getTranslations("twoFactor.verify");

  return (
    <>
      <AuthTitle>{t("title")}</AuthTitle>
      <TwoFactorVerifyForm />
    </>
  );
}
