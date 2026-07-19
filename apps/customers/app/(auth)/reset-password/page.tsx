import { getTranslations } from "next-intl/server";
import { Suspense } from "react";
import { AuthTitle } from "../auth-title";
import { ResetPasswordForm } from "./reset-password-form";

export default async function ResetPasswordPage() {
  const t = await getTranslations("resetPassword");

  return (
    <>
      <AuthTitle>{t("title")}</AuthTitle>
      <Suspense>
        <ResetPasswordForm />
      </Suspense>
    </>
  );
}
