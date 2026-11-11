import {
  ActionIcon,
  Alert,
  Badge,
  Box,
  Card,
  Group,
  Select,
  SimpleGrid,
  Stack,
  Table,
  Text,
  TextInput,
} from "@mantine/core";
import { IconAlertCircle, IconAlertTriangle, IconChevronDown, IconChevronRight } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { useNavigate, useParams, useSearch } from "@tanstack/react-router";
import { ContentSkeleton, PageHeader, PageSection } from "@vantigo/frontend-shell";
import { useState } from "react";
import { bomOverviewQueryOptions, bomQueryOptions, type ManufacturingOverviewNode } from "../api/boms";
import { manufacturingMetaQueryOptions } from "../api/meta";
import { manufacturingSitesQueryOptions } from "../api/sites";
import { isDecimal, isPositiveDecimal, normalizeDecimal } from "../lib/decimal";
import { refusalMessage, useManufacturingFormat } from "../lib/format";
import { BOM_ROUTE_PATH, BOMS_ROUTE_PATH, bomOverviewLinkOptions, MANUFACTURING_HOME_ROUTE_PATH } from "../lib/routes";
import type { BomOverviewSearch } from "../lib/search";

/** How many levels are open when the overview is first drawn: the BoM and its own components. */
const OPEN_DEPTH = 1;

/** Whether the server answered a field: cost fields are left out, not null, without costs-view (M38). */
const has = <T extends object>(value: T, key: keyof T) => Object.hasOwn(value, key);

const OverviewNode = ({ node, currency }: { node: ManufacturingOverviewNode; currency: string }) => {
  const { t, quantity, minutes, money, decimal } = useManufacturingFormat();
  const [open, setOpen] = useState(node.depth < OPEN_DEPTH);
  const expandable = node.children.length > 0 || node.operations.length > 0;
  const name = `${node.variant.sku} · ${node.variant.productName}`;

  return (
    <Box data-node={node.variant.sku}>
      <Group
        gap="xs"
        wrap="nowrap"
        align="flex-start"
        py={6}
        pl={node.depth * 24}
        style={{ borderBottom: "1px solid var(--mantine-color-default-border)" }}
      >
        <Box w={28}>
          {expandable && (
            <ActionIcon
              variant="subtle"
              size="sm"
              aria-label={t(open ? "collapseNode" : "expandNode", { name })}
              aria-expanded={open}
              onClick={() => setOpen((current) => !current)}
            >
              {open ? <IconChevronDown size={16} /> : <IconChevronRight size={16} />}
            </ActionIcon>
          )}
        </Box>
        <Stack gap={2} style={{ flex: 1, minWidth: 0 }}>
          <Group gap="xs">
            <Text fw={600} size="sm">
              {name}
            </Text>
            <Badge size="sm" variant="light" color={node.made ? "violet" : "gray"}>
              {t(node.made ? "made" : "bought")}
            </Badge>
          </Group>
          <Group gap="lg">
            <Text size="sm">
              {t("requiredQuantity", { quantity: quantity(node.quantityRequired, node.variant.unit) })}
            </Text>
            {isPositiveDecimal(node.scrapPercent) && (
              <Text size="sm" c="dimmed">
                {t("scrapValue", { value: decimal(node.scrapPercent) })}
              </Text>
            )}
            {node.available !== null && (
              <Text size="sm" c="dimmed">
                {t("availableQuantity", { quantity: quantity(node.available, node.variant.unit) })}
              </Text>
            )}
            {has(node, "standardCost") && (
              <Text size="sm" c="dimmed">
                {node.standardCost == null
                  ? t("noStandardCost")
                  : t("standardCostValue", { amount: money(node.standardCost, currency) })}
              </Text>
            )}
            {has(node, "extendedValue") && node.extendedValue != null && (
              <Text size="sm" c="dimmed">
                {t("extendedValue", { amount: money(node.extendedValue, currency) })}
              </Text>
            )}
          </Group>
          {open && node.operations.length > 0 && (
            <Table withRowBorders={false} verticalSpacing={2} fz="sm">
              <Table.Tbody>
                {node.operations.map((operation) => (
                  <Table.Tr key={operation.operationId}>
                    <Table.Td w={60}>{operation.sequence}</Table.Td>
                    <Table.Td>{operation.name}</Table.Td>
                    <Table.Td>{operation.workCenterCode}</Table.Td>
                    <Table.Td>{minutes(operation.expectedMinutes)}</Table.Td>
                    {has(operation, "cost") && (
                      <Table.Td>{operation.cost == null ? "" : money(operation.cost, currency)}</Table.Td>
                    )}
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          )}
        </Stack>
      </Group>
      {open &&
        node.children.map((child, index) => (
          // A component can be on two lines; its place among its siblings tells them apart.
          <OverviewNode key={`${child.variant.id}-${index}`} node={child} currency={currency} />
        ))}
    </Box>
  );
};

/**
 * The BoM exploded (M15): at a quantity — the output quantity unless another
 * is typed — and, when a site is chosen, with what is available there. Each
 * component says whether it is made (through its default active BoM) or
 * bought, what is required including scrap, and a made one its operations'
 * expected time. The standard costs, the extended values and the rolled-up
 * cost show only when the server answers them (`manufacturing:costs-view`).
 */
export const BomOverviewPage = () => {
  const { t, money } = useManufacturingFormat();
  const { bomId } = useParams({ strict: false }) as { bomId: string };
  const search = useSearch({ strict: false }) as BomOverviewSearch;
  const navigate = useNavigate() as (options: unknown) => Promise<void>;
  const id = Number(bomId);
  const meta = useQuery(manufacturingMetaQueryOptions());
  const bom = useQuery(bomQueryOptions(id));
  const sites = useQuery(manufacturingSitesQueryOptions());
  const overview = useQuery(bomOverviewQueryOptions(id, search.quantity, search.siteId));
  const [typed, setTyped] = useState(search.quantity ?? "");
  const [quantityError, setQuantityError] = useState<string>();
  const currency = meta.data?.currency ?? "NOK";

  const filter = (patch: Partial<BomOverviewSearch>) =>
    void navigate({ ...bomOverviewLinkOptions(id, { ...search, ...patch }), replace: true });
  const applyQuantity = () => {
    const text = typed.trim();
    if (text === "") {
      setQuantityError(undefined);
      return filter({ quantity: undefined });
    }
    if (!isDecimal(text) || !isPositiveDecimal(text)) return setQuantityError(t("problem_quantity"));
    setQuantityError(undefined);
    filter({ quantity: normalizeDecimal(text) });
  };

  const name = bom.data ? `${bom.data.variant.sku}${bom.data.code ? ` · ${bom.data.code}` : ""}` : t("bom");
  const data = overview.data;

  return (
    <Stack gap="lg">
      <PageHeader
        breadcrumbs={[
          { label: t("manufacturing"), to: MANUFACTURING_HOME_ROUTE_PATH },
          { label: t("navBoms"), to: BOMS_ROUTE_PATH },
          { label: name, to: BOM_ROUTE_PATH.replace("$bomId", String(id)) },
          { label: t("bomOverview") },
        ]}
        title={t("bomOverviewTitle", { name })}
        description={t("bomOverviewDescription")}
      />
      <PageSection>
        <SimpleGrid cols={{ base: 1, sm: 2 }} data-assist="manufacturing.bom-overview.inputs">
          <TextInput
            label={t("overviewQuantity")}
            description={bom.data ? t("overviewQuantityHint", { quantity: bom.data.outputQuantity }) : undefined}
            inputMode="decimal"
            placeholder={bom.data?.outputQuantity}
            value={typed}
            onChange={(event) => setTyped(event.currentTarget.value)}
            onBlur={applyQuantity}
            onKeyDown={(event) => {
              if (event.key !== "Enter") return;
              event.preventDefault();
              applyQuantity();
            }}
            error={quantityError}
          />
          <Select
            label={t("site")}
            description={t("overviewSiteHint")}
            placeholder={t("noSite")}
            clearable
            data={(sites.data ?? []).map((site) => ({ value: String(site.id), label: `${site.code} · ${site.name}` }))}
            value={search.siteId === undefined ? null : String(search.siteId)}
            onChange={(next) => filter({ siteId: next === null ? undefined : Number(next) })}
          />
        </SimpleGrid>
      </PageSection>
      {overview.error ? (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
          {refusalMessage(t, overview.error)}
        </Alert>
      ) : !data ? (
        <ContentSkeleton rows={4} />
      ) : (
        <>
          {data.rolledUpCost && (
            <SimpleGrid cols={{ base: 1, sm: 3 }} data-assist="manufacturing.bom-overview.cost">
              <Card withBorder padding="md">
                <Text size="sm" c="dimmed">
                  {t("componentsCost")}
                </Text>
                <Text fw={600}>{money(data.rolledUpCost.components, currency)}</Text>
              </Card>
              <Card withBorder padding="md">
                <Text size="sm" c="dimmed">
                  {t("operationsCost")}
                </Text>
                <Text fw={600}>{money(data.rolledUpCost.operations, currency)}</Text>
              </Card>
              <Card withBorder padding="md">
                <Text size="sm" c="dimmed">
                  {t("rolledUpCost")}
                </Text>
                <Text fw={600}>{money(data.rolledUpCost.total, currency)}</Text>
              </Card>
            </SimpleGrid>
          )}
          {data.costIncomplete === true && (
            <Alert color="yellow" icon={<IconAlertTriangle size={16} />} title={t("costIncomplete")}>
              {t("costIncompleteHint")}
            </Alert>
          )}
          <PageSection title={t("structure")}>
            <Stack gap={0} data-assist="manufacturing.bom-overview.tree">
              <Text size="sm" c="dimmed" mb="xs">
                {t("nodeCount", { count: data.nodeCount })}
              </Text>
              <OverviewNode node={data.root} currency={currency} />
            </Stack>
          </PageSection>
        </>
      )}
    </Stack>
  );
};
