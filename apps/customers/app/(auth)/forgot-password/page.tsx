import { getTranslations } from "next-intl/server";
import { AuthTitle } from "../auth-title";
import { ForgotPasswordForm } from "./forgot-password-form";

export default async function ForgotPasswordPage() {
  const t = await getTranslations("forgotPassword");

  return (
    <>
      <AuthTitle>{t("title")}</AuthTitle>
      <ForgotPasswordForm />
    </>
  );
}
