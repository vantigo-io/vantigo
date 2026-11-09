import { Anchor, Badge, Checkbox, Group, Stack, Table, Text } from "@mantine/core";
import { Link } from "@tanstack/react-router";
import type { InventoryLowStockRow } from "../api/placements";
import { useInventoryFormat } from "../lib/format";
import { lowStockKey } from "../lib/low-stock";
import { variantStockLinkOptions } from "../lib/routes";
import { SuggestionNote } from "./suggestion-note";

export interface LowStockListProps {
  rows: readonly InventoryLowStockRow[];
  /** The rows chosen, by `lowStockKey`; no checkboxes are drawn without it. */
  selected?: ReadonlySet<string>;
  onToggle?: (row: InventoryLowStockRow, checked: boolean) => void;
  /** The site the chosen rows are at: rows at any other cannot be ticked. */
  chosenSiteId?: number;
  /** Whether the site column is drawn: the list is not filtered to one site. */
  showSite: boolean;
}

/**
 * The low placements (I58; R §7.3): the item, what is available and on its
 * way, the reorder point and the maximum, the suggestion with its rounding
 * sentence (I57), each other site's available, and *Draft N open* while a
 * draft to the site holds the variant (I59). Rows are chosen at one site only:
 * a row at another site than the chosen ones cannot be ticked.
 */
export const LowStockList = ({ rows, selected, onToggle, chosenSiteId, showSite }: LowStockListProps) => {
  const { t, quantity, number } = useInventoryFormat();
  const selecting = selected !== undefined && onToggle !== undefined;
  const dash = (
    <Text span c="dimmed">
      —
    </Text>
  );

  return (
    <Table.ScrollContainer minWidth={900}>
      <Table striped verticalSpacing="xs">
        <Table.Thead>
          <Table.Tr>
            {selecting && <Table.Th w={40} />}
            <Table.Th>{t("columnItem")}</Table.Th>
            {showSite && <Table.Th>{t("site")}</Table.Th>}
            <Table.Th ta="right">{t("available")}</Table.Th>
            <Table.Th ta="right">{t("incoming")}</Table.Th>
            <Table.Th ta="right">{t("reorderPoint")}</Table.Th>
            <Table.Th ta="right">{t("maximumLevel")}</Table.Th>
            <Table.Th>{t("suggestedQuantity")}</Table.Th>
            <Table.Th>{t("elsewhere")}</Table.Th>
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {rows.map((row) => {
            const key = lowStockKey(row);
            const unit = row.variant.unit;
            return (
              <Table.Tr key={key} data-selected={selected?.has(key) ? "true" : undefined}>
                {selecting && (
                  <Table.Td>
                    <Checkbox
                      aria-label={t("selectLowStockRow", { sku: row.variant.sku, site: row.site.code })}
                      checked={selected.has(key)}
                      disabled={chosenSiteId !== undefined && chosenSiteId !== row.site.id}
                      onChange={(event) => onToggle(row, event.currentTarget.checked)}
                    />
                  </Table.Td>
                )}
                <Table.Td>
                  <Stack gap={2}>
                    <Text size="sm">
                      <Anchor
                        size="sm"
                        renderRoot={(props) => <Link {...variantStockLinkOptions(row.variant.id)} {...props} />}
                      >
                        {row.variant.sku}
                      </Anchor>
                      <Text span size="sm" c="dimmed">
                        {" · "}
                        {row.variant.productName}
                      </Text>
                    </Text>
                    {row.openDrafts.length > 0 && (
                      <Group gap={4}>
                        {row.openDrafts.map((draft) => (
                          <Badge key={draft} size="xs" variant="light" color="gray">
                            {t("draftOpen", { number: draft })}
                          </Badge>
                        ))}
                      </Group>
                    )}
                  </Stack>
                </Table.Td>
                {showSite && <Table.Td>{row.site.code}</Table.Td>}
                <Table.Td ta="right">
                  <Text size="sm" c={row.available < 0 ? "red" : undefined}>
                    {number(row.available)}
                  </Text>
                </Table.Td>
                <Table.Td ta="right">{row.incoming > 0 ? number(row.incoming) : dash}</Table.Td>
                <Table.Td ta="right">{number(row.reorderPoint)}</Table.Td>
                <Table.Td ta="right">{row.maximum === null ? dash : number(row.maximum)}</Table.Td>
                <Table.Td miw={200}>
                  <Text size="sm" fw={500}>
                    {quantity(row.suggested, unit)}
                  </Text>
                  <SuggestionNote
                    needed={row.needed}
                    suggested={row.suggested}
                    packaging={row.packaging}
                    maximum={row.maximum}
                  />
                </Table.Td>
                <Table.Td>
                  {row.elsewhere.length === 0 ? (
                    dash
                  ) : (
                    <Text size="sm">
                      {row.elsewhere.map((one) => `${one.siteName} ${number(one.available)}`).join(" · ")}
                    </Text>
                  )}
                </Table.Td>
              </Table.Tr>
            );
          })}
        </Table.Tbody>
      </Table>
    </Table.ScrollContainer>
  );
};
