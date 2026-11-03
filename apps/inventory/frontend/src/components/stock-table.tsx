import { Anchor, Table, Text } from "@mantine/core";
import { Link } from "@tanstack/react-router";
import type { InventoryBalanceRow } from "../api/locations";
import { useInventoryFormat } from "../lib/format";
import { locationLinkOptions, variantStockLinkOptions } from "../lib/routes";

export interface StockTableProps {
  rows: readonly InventoryBalanceRow[];
  /** Leave out the variant's columns, on a page that is about one variant. */
  hideVariant?: boolean;
  /** Leave out the location's column, on a page that is about one location alone. */
  hideLocation?: boolean;
}

/** Balance rows (I10): the variant, its SKU and product, the location it lies on and the quantity there. */
export const StockTable = ({ rows, hideVariant, hideLocation }: StockTableProps) => {
  const { t, quantity } = useInventoryFormat();
  return (
    <Table.ScrollContainer minWidth={520}>
      <Table striped verticalSpacing="xs">
        <Table.Thead>
          <Table.Tr>
            {!hideVariant && <Table.Th>{t("columnSku")}</Table.Th>}
            {!hideVariant && <Table.Th>{t("columnProduct")}</Table.Th>}
            {!hideLocation && <Table.Th>{t("columnLocation")}</Table.Th>}
            <Table.Th ta="right">{t("columnOnHand")}</Table.Th>
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {rows.map((row) => (
            <Table.Tr key={`${row.variant.id}-${row.location.id}`}>
              {!hideVariant && (
                <Table.Td>
                  <Anchor
                    size="sm"
                    renderRoot={(props) => <Link {...variantStockLinkOptions(row.variant.id)} {...props} />}
                  >
                    {row.variant.sku}
                  </Anchor>
                </Table.Td>
              )}
              {!hideVariant && <Table.Td>{row.variant.productName}</Table.Td>}
              {!hideLocation && (
                <Table.Td>
                  <Anchor
                    size="sm"
                    ff="monospace"
                    renderRoot={(props) => <Link {...locationLinkOptions(row.location.id)} {...props} />}
                  >
                    {row.location.fullCode}
                  </Anchor>
                  {row.location.name && (
                    <Text span size="sm" c="dimmed">
                      {" "}
                      {row.location.name}
                    </Text>
                  )}
                </Table.Td>
              )}
              <Table.Td ta="right">{quantity(row.onHand, row.variant.unit)}</Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
    </Table.ScrollContainer>
  );
};
