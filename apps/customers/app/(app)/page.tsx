import { SimpleGrid, Title } from "@mantine/core";
import { StatCard } from "@vantigo/customers/lib/components/stat-card";
import { getTranslations } from "next-intl/server";

export default async function DashboardPage() {
  const t = await getTranslations("dashboard");

  return (
    <>
      <Title order={2} mb="lg">
        {t("title")}
      </Title>

      <SimpleGrid cols={{ base: 1, sm: 2, lg: 4 }}>
        <StatCard
          label={t("totalCustomers")}
          value="1 204"
          diff={12.3}
          description={t("diffDescription")}
        />
        <StatCard
          label={t("revenue")}
          value="kr 84 500"
          diff={-4.1}
          description={t("diffDescription")}
        />
        <StatCard
          label={t("openInvoices")}
          value="23"
          diff={2.0}
          description={t("diffDescription")}
        />
        <StatCard label={t("activeProjects")} value="7" />
      </SimpleGrid>
    </>
  );
}
