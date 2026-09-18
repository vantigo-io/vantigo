import { ActionIcon, Alert, Badge, Box, Button, Card, Group, SimpleGrid, Stack, Table, Text } from "@mantine/core";
import { IconAlertCircle, IconLock, IconPencil, IconPlus } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { ContentSkeleton, EmptyState, type LocaleFormatters, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { type BillingLine, billingLinesQueryOptions } from "../api/lines";
import { type Project, projectQueryOptions } from "../api/projects";
import { Field } from "../components/field";
import "../i18n";
import { billingTypeLabelKey, pricingModeLabelKey } from "../lib/billing";
import { BillingLineFormModal, type BillingLineModalState } from "./-billing-line-form-modal";

/**
 * The Billing tab (design §8.2). Financial fields are shaped out of the
 * response for a caller who may not see them (D12), so the tab asks the
 * project first and reads nothing else when the answer is no.
 */
export const ProjectBilling = ({ projectId }: { projectId: number }) => {
  const { t } = useI18n("projects");
  const { data: project, isPending, isError, error } = useQuery(projectQueryOptions(projectId));

  if (isError) {
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadProject")} mt="md">
        {error.message}
      </Alert>
    );
  }
  if (isPending)
    return (
      <Box mt="md">
        <ContentSkeleton rows={4} rowHeight={48} />
      </Box>
    );

  if (!project.capabilities.canSeeFinancials) {
    return (
      <Box mt="md">
        <EmptyState icon={IconLock} title={t("financialsHidden")} description={t("financialsHiddenDescription")} />
      </Box>
    );
  }

  return (
    <Stack gap="lg" mt="md">
      <FinancialSummary project={project} />
      {project.billingLinesAvailable ? (
        <BillingLinesCard projectId={projectId} project={project} />
      ) : (
        <Text size="sm" c="dimmed">
          {t("billingLinesUnavailable")}
        </Text>
      )}
    </Stack>
  );
};

const FinancialSummary = ({ project }: { project: Project }) => {
  const { t, formatters } = useI18n("projects");
  const currency = project.financials?.currency ?? undefined;
  const money = (value: number | null | undefined) =>
    value === null || value === undefined
      ? t("notAvailable")
      : currency
        ? formatters.formatCurrency(value, currency)
        : formatters.formatNumber(value);

  return (
    <Card withBorder padding="lg" radius="md" data-testid="financial-summary">
      <Stack gap="md">
        <Text fw={600} component="h3">
          {t("financialSummary")}
        </Text>
        <SimpleGrid cols={{ base: 1, sm: 2, md: 4 }} spacing="md">
          <Field label={t("billingType")}>{t(billingTypeLabelKey(project.billingType))}</Field>
          <Field label={t("fixedPriceAmount")}>{money(project.financials?.fixedPriceAmount)}</Field>
          <Field label={t("budgetAmount")}>{money(project.financials?.budgetAmount)}</Field>
          <Field label={t("currency")}>{currency ?? t("notAvailable")}</Field>
        </SimpleGrid>
      </Stack>
    </Card>
  );
};

const BillingLinesCard = ({ projectId, project }: { projectId: number; project: Project }) => {
  const { t } = useI18n("projects");
  const { data: lines, isPending, isError, error } = useQuery(billingLinesQueryOptions(projectId));
  const [modalState, setModalState] = useState<BillingLineModalState | null>(null);
  const canManage = project.capabilities.canManage;

  return (
    <Card withBorder padding="lg" radius="md">
      <Stack gap="md">
        <Group justify="space-between" wrap="wrap">
          <Stack gap={2}>
            <Text fw={600} component="h3">
              {t("billingLines")}
            </Text>
            <Text size="sm" c="dimmed">
              {t("billingLinesDescription")}
            </Text>
          </Stack>
          {canManage && (
            <Button size="xs" leftSection={<IconPlus size={14} />} onClick={() => setModalState({ mode: "create" })}>
              {t("addBillingLine")}
            </Button>
          )}
        </Group>

        {isError && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadLines")}>
            {error.message}
          </Alert>
        )}
        {isPending && <ContentSkeleton rows={3} rowHeight={48} />}
        {lines && lines.length === 0 && <EmptyState title={t("noBillingLines")} size="sm" />}

        {lines && lines.length > 0 && (
          <Table.ScrollContainer minWidth={760}>
            <Table striped highlightOnHover>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("trackableCode")}</Table.Th>
                  <Table.Th>{t("product")}</Table.Th>
                  <Table.Th>{t("unit")}</Table.Th>
                  <Table.Th>{t("pricingMode")}</Table.Th>
                  <Table.Th>{t("listPrice")}</Table.Th>
                  <Table.Th>{t("status")}</Table.Th>
                  {canManage && <Table.Th>{t("actions")}</Table.Th>}
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {lines.map((line) => (
                  <LineRow
                    key={line.id}
                    line={line}
                    currency={project.financials?.currency ?? undefined}
                    canManage={canManage}
                    onEdit={setModalState}
                  />
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}
      </Stack>

      <BillingLineFormModal
        projectId={projectId}
        currency={project.financials?.currency ?? undefined}
        state={modalState}
        onClose={() => setModalState(null)}
      />
    </Card>
  );
};

const LineRow = ({
  line,
  currency,
  canManage,
  onEdit,
}: {
  line: BillingLine;
  /** The project's currency: what a fixed amount on a line is expressed in. */
  currency?: string;
  canManage: boolean;
  onEdit: (state: BillingLineModalState) => void;
}) => {
  const { t, formatters } = useI18n("projects");
  const listPrice = line.pricing?.listPrice;

  return (
    <Table.Tr>
      <Table.Td>
        <Text size="sm" ff="monospace" fw={600}>
          {line.trackableCode}
        </Text>
      </Table.Td>
      <Table.Td>
        {line.variantMissing || !line.productName ? (
          <Text size="sm" c="dimmed">
            {t("unknownProduct")}
          </Text>
        ) : (
          <Text size="sm">{line.productName}</Text>
        )}
      </Table.Td>
      <Table.Td>
        <Text size="sm">{line.unit ?? t("notAvailable")}</Text>
      </Table.Td>
      <Table.Td>
        <Text size="sm">{pricingRule(t, formatters, line, currency)}</Text>
      </Table.Td>
      <Table.Td>
        <Text size="sm">
          {listPrice ? formatters.formatCurrency(listPrice.amount, listPrice.currency) : t("notAvailable")}
        </Text>
      </Table.Td>
      <Table.Td>
        <Badge variant="light" color={line.active ? "teal" : "gray"}>
          {line.active ? t("active") : t("inactive")}
        </Badge>
      </Table.Td>
      {canManage && (
        <Table.Td>
          <ActionIcon variant="subtle" aria-label={t("editBillingLine")} onClick={() => onEdit({ mode: "edit", line })}>
            <IconPencil size={16} />
          </ActionIcon>
        </Table.Td>
      )}
    </Table.Tr>
  );
};

/**
 * The rule the line is priced by, in words: the list price as it stands, an
 * amount that replaces it, or a percentage off it. Pricing is absent
 * altogether for a caller who may not see the project's amounts (D12), which
 * this tab has already turned away.
 */
const pricingRule = (
  t: (key: string, values?: Record<string, unknown>) => string,
  formatters: Pick<LocaleFormatters, "formatCurrency" | "formatNumber">,
  line: BillingLine,
  currency: string | undefined,
): string => {
  const pricing = line.pricing;
  if (!pricing) return t("notAvailable");
  if (pricing.mode === "fixed" && pricing.fixedAmount !== undefined && pricing.fixedAmount !== null) {
    // A project without a currency cannot carry a fixed line, but a line
    // written before one was cleared still has to render as a number.
    return t("pricingFixedValue", {
      amount: currency
        ? formatters.formatCurrency(pricing.fixedAmount, currency)
        : formatters.formatNumber(pricing.fixedAmount),
    });
  }
  if (pricing.mode === "discount" && pricing.discountPercent !== undefined && pricing.discountPercent !== null) {
    return t("pricingDiscountValue", { percent: formatters.formatNumber(pricing.discountPercent) });
  }
  return t(pricingModeLabelKey(pricing.mode));
};
