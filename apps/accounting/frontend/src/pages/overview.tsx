import { Alert, Anchor, Button, Card, Group, SimpleGrid, Stack, Text, Title } from "@mantine/core";
import {
  IconAlertCircle,
  IconAlertTriangle,
  IconChecks,
  IconFileSpreadsheet,
  IconList,
  IconPlus,
  IconReceiptTax,
  IconRefresh,
  IconScale,
} from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ContentSkeleton, KpiCard, PageHeader } from "@vantigo/frontend-shell";
import { type AccountingMeta, catchUpQueryOptions, metaQueryOptions } from "../api/accounting";
import { NotSetUp } from "../components/books-gate";
import { useAccountingFormat } from "../lib/format";
import {
  JOURNAL_ROUTE_PATH,
  NEEDS_ATTENTION_ROUTE_PATH,
  OPEN_ITEMS_ROUTE_PATH,
  OPENING_ROUTE_PATH,
  REPORTS_ROUTE_PATH,
  SETTINGS_ROUTE_PATH,
  VOUCHERS_ROUTE_PATH,
  vatTermPath,
} from "../lib/routes";

/**
 * The app's home: whether the books are set up, the lock date, the year's
 * vouchers and the open customer items, what needs attention — the sales
 * facts the books could not post (A15) — where the catch-up of sales stands
 * (A12), the next VAT term due (A22), and the shortcuts. Until the books are set up it shows only the way there (R12).
 */
export const AccountingOverviewPage = () => {
  const { t, day } = useAccountingFormat();
  const meta = useQuery(metaQueryOptions());

  if (meta.error)
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
        {meta.error.message}
      </Alert>
    );
  if (meta.isPending) return <ContentSkeleton rows={2} rowHeight={120} />;
  const m = meta.data;
  if (!m.setUp || !m.settings)
    return (
      <Stack gap="lg">
        <PageHeader title={t("overviewTitle")} description={t("overviewDescription")} />
        <NotSetUp meta={m} />
      </Stack>
    );
  const s = m.settings;
  return (
    <Stack gap="lg">
      <PageHeader
        title={t("overviewTitle")}
        description={t("booksSince", { date: day(s.booksStart) })}
        actions={
          m.canPost && (
            <Button
              data-assist="accounting.overview.new-entry"
              leftSection={<IconPlus size={16} />}
              renderRoot={(props) => <Link to={JOURNAL_ROUTE_PATH} {...props} />}
            >
              {t("newJournalEntry")}
            </Button>
          )
        }
      />
      <SimpleGrid cols={{ base: 1, xs: 3 }} spacing="md" data-assist="accounting.overview.cards">
        <KpiCard label={t("vouchersThisYear")} value={m.vouchersThisYear} href={VOUCHERS_ROUTE_PATH} />
        <KpiCard label={t("openCustomerItems")} value={m.openCustomerItems} href={OPEN_ITEMS_ROUTE_PATH} />
        <KpiCard
          label={t("lockDate")}
          value={s.lockedThrough ? day(s.lockedThrough) : t("notLocked")}
          href={SETTINGS_ROUTE_PATH}
        />
      </SimpleGrid>
      <Card withBorder padding="md" data-testid="needs-attention-card">
        <Group gap="xs" mb="xs">
          {m.unposted > 0 ? (
            <IconAlertTriangle size={18} color="var(--mantine-color-orange-6)" />
          ) : (
            <IconChecks size={18} />
          )}
          <Title order={4}>{t("needsAttention")}</Title>
        </Group>
        {m.unposted > 0 ? (
          <Anchor component={Link} to={NEEDS_ATTENTION_ROUTE_PATH} size="sm">
            {t("needsAttentionCount", { count: m.unposted })}
          </Anchor>
        ) : (
          <Text c="dimmed" size="sm">
            {t("nothingNeedsAttention")}
          </Text>
        )}
      </Card>
      {m.vatRegistrationMismatch && (
        <Alert color="orange" icon={<IconAlertTriangle size={16} />} data-testid="vat-mismatch">
          {t("vatRegistrationMismatch")}
        </Alert>
      )}
      {m.vatNextTerm && <VatCard meta={m} />}
      {m.invoicesEnabled && <CatchUpCard />}
      <Group gap="sm">
        <Button
          variant="light"
          leftSection={<IconList size={16} />}
          renderRoot={(props) => <Link to={VOUCHERS_ROUTE_PATH} {...props} />}
        >
          {t("navVouchers")}
        </Button>
        <Button
          variant="light"
          leftSection={<IconFileSpreadsheet size={16} />}
          renderRoot={(props) => <Link to={REPORTS_ROUTE_PATH} {...props} />}
        >
          {t("navReports")}
        </Button>
        <Button
          variant="light"
          leftSection={<IconScale size={16} />}
          renderRoot={(props) => <Link to={OPENING_ROUTE_PATH} {...props} />}
        >
          {t("openingBalances")}
        </Button>
      </Group>
    </Stack>
  );
};

/** The next VAT term due and its state (A22), with what waits to be closed or filed. */
const VatCard = ({ meta }: { meta: AccountingMeta }) => {
  const { t, day } = useAccountingFormat();
  const term = meta.vatNextTerm;
  if (!term) return null;
  const state = term.filedAt ? t("vatStateFiled") : term.state === "closed" ? t("vatStateClosed") : t("vatStateOpen");
  return (
    <Card withBorder padding="md" data-testid="vat-card">
      <Group gap="xs" mb="xs">
        {meta.vatTermsToFile > 0 || meta.vatTermsToClose > 0 ? (
          <IconAlertTriangle size={18} color="var(--mantine-color-orange-6)" />
        ) : (
          <IconReceiptTax size={18} />
        )}
        <Title order={4}>{t("vatNextTerm")}</Title>
      </Group>
      <Stack gap={4}>
        <Anchor component={Link} to={vatTermPath(term.year, term.number)} size="sm">
          {t("vatNextTermLine", {
            term: t("vatTermName", { year: term.year, number: term.number, period: term.periodName }),
            state,
            due: day(term.dueDate),
          })}
        </Anchor>
        {meta.vatTermsToClose > 0 && <Text size="sm">{t("vatTermsToClose", { count: meta.vatTermsToClose })}</Text>}
        {meta.vatTermsToFile > 0 && (
          <Text size="sm" c="red">
            {t("vatTermsToFile", { count: meta.vatTermsToFile })}
          </Text>
        )}
      </Stack>
    </Card>
  );
};

/** Where the catch-up of Invoices' sales stands (A12, R7): per stream, how far it has read, what waits, its last run. */
const CatchUpCard = () => {
  const { t, stamp } = useAccountingFormat();
  const status = useQuery(catchUpQueryOptions());
  if (!status.data) return null;
  return (
    <Card withBorder padding="md" data-testid="catch-up-card">
      <Group gap="xs" mb="xs">
        <IconRefresh size={18} />
        <Title order={4}>{t("catchUp")}</Title>
      </Group>
      <Stack gap={4}>
        {status.data.streams.map((s) => (
          <Text size="sm" key={s.stream}>
            <b>{t(`catchUpStream_${s.stream}`)}</b> · {t("catchUpPosition", { position: s.position })} ·{" "}
            {s.behind === null
              ? t("catchUpUnknown")
              : s.behind > 0
                ? t("catchUpBehind", { count: s.behindMore ? `${s.behind}+` : s.behind })
                : t("catchUpUpToDate")}{" "}
            · {s.lastRunAt ? t("catchUpLastRun", { at: stamp(s.lastRunAt) }) : t("catchUpNeverRun")}
            {s.lastError && (
              <Text span c="red" size="sm">
                {" "}
                · {t("catchUpError", { error: s.lastError })}
              </Text>
            )}
          </Text>
        ))}
      </Stack>
    </Card>
  );
};
