import { Alert, Button, Card, Select, Stack, Text, TextInput } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { ContentSkeleton, PageHeader } from "@vantigo/frontend-shell";
import { useState } from "react";
import { type AccountingVatTerm, metaQueryOptions, setUpBooks, vatTerms } from "../api/accounting";
import { ACCOUNTING_QUERY_KEY, refusedFields } from "../api/request";
import { refusalMessage, useAccountingFormat } from "../lib/format";
import { ACCOUNTING_HOME_ROUTE_PATH, OPENING_ROUTE_PATH } from "../lib/routes";

/**
 * Set up the books (R12): the first day the books cover, the fiscal year's
 * first month, the VAT term and the language the default chart is named in.
 * It seeds the chart, the tax codes and the system roles, once.
 */
export const SetupPage = () => {
  const { t, locale } = useAccountingFormat();
  const meta = useQuery(metaQueryOptions());
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const thisYear = new Date().getFullYear();
  const [booksStart, setBooksStart] = useState(`${thisYear}-01-01`);
  const [month, setMonth] = useState<string | null>("1");
  const [vatTerm, setVatTerm] = useState<string | null>("bimonthly");
  const [language, setLanguage] = useState<string | null>(locale === "nb" ? "nb" : "en");
  const setup = useMutation({
    mutationFn: () =>
      setUpBooks({
        booksStart,
        fiscalYearStartMonth: Number(month ?? 1),
        vatTerm: (vatTerm ?? "bimonthly") as AccountingVatTerm,
        language: language === "nb" ? "nb" : "en",
      }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [ACCOUNTING_QUERY_KEY] });
      notifications.show({ color: "green", message: t("booksSetUp") });
      await navigate({ to: OPENING_ROUTE_PATH });
    },
  });
  const fields = refusedFields(setup.error);

  if (meta.isPending) return <ContentSkeleton rows={3} />;
  if (meta.data?.setUp)
    return (
      <Stack gap="lg">
        <PageHeader title={t("setUpTheBooks")} />
        <Alert color="blue" title={t("alreadySetUp")}>
          <Button variant="light" renderRoot={(props) => <Link to={ACCOUNTING_HOME_ROUTE_PATH} {...props} />}>
            {t("navOverview")}
          </Button>
        </Alert>
      </Stack>
    );
  const months = Array.from({ length: 12 }, (_, i) => ({ value: String(i + 1), label: t(`month_${i + 1}`) }));
  return (
    <Stack gap="lg">
      <PageHeader title={t("setUpTheBooks")} description={t("setUpDescription")} />
      <Card withBorder padding="lg" maw={560}>
        <form
          onSubmit={(event) => {
            event.preventDefault();
            setup.mutate();
          }}
        >
          <Stack gap="md">
            <TextInput
              type="date"
              required
              label={t("booksStart")}
              description={t("booksStartHint")}
              value={booksStart}
              onChange={(event) => setBooksStart(event.currentTarget.value)}
              error={fields.booksStart}
            />
            <Select
              label={t("fiscalYearStartMonth")}
              data={months}
              value={month}
              onChange={setMonth}
              allowDeselect={false}
              error={fields.fiscalYearStartMonth}
            />
            <Select
              label={t("vatTerm")}
              description={t("vatTermHint")}
              data={vatTerms.map((term) => ({ value: term, label: t(`vatTerm_${term}`) }))}
              value={vatTerm}
              onChange={setVatTerm}
              allowDeselect={false}
              error={fields.vatTerm}
            />
            <Select
              label={t("chartLanguage")}
              description={t("chartLanguageHint")}
              data={[
                { value: "nb", label: t("language_nb") },
                { value: "en", label: t("language_en") },
              ]}
              value={language}
              onChange={setLanguage}
              allowDeselect={false}
              error={fields.language}
            />
            <Text size="sm" c="dimmed">
              {t("setUpFinal")}
            </Text>
            {setup.error && Object.keys(fields).length === 0 && (
              <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotSave")}>
                {refusalMessage(t, setup.error)}
              </Alert>
            )}
            <Button
              type="submit"
              loading={setup.isPending}
              disabled={!meta.data?.canClose}
              data-assist="accounting.setup.submit"
            >
              {t("setUpTheBooks")}
            </Button>
          </Stack>
        </form>
      </Card>
    </Stack>
  );
};
