import { Title } from "@mantine/core";
import { getTranslations } from "next-intl/server";

export default async function CustomersPage() {
  const t = await getTranslations("customers");

  return <Title order={2}>{t("title")}</Title>;
}
