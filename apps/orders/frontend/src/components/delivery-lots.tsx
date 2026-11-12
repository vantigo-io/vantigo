import { ActionIcon, Badge, Checkbox, Group, NumberInput, Select, Stack, Table, Text } from "@mantine/core";
import { IconTrash } from "@tabler/icons-react";
import { useState } from "react";
import type { DeliveryLine } from "../api/types";
import { type LotPickRow, lotsTotal } from "../lib/delivery-lots";
import { useOrdersFormat } from "../lib/format";

export interface DeliveryLotsProps {
  line: DeliveryLine;
  /** The line's place in the delivery, from 1, for the inputs' names. */
  position: number;
  picks: readonly LotPickRow[];
  /** The line's quantity as the editor holds it. */
  quantity: number;
  editable: boolean;
  /** The caller holds `inventory:expired-override`: *Use expired lot* is offered. */
  canOverrideExpired: boolean;
  onChange: (picks: LotPickRow[]) => void;
  /** A refusal's words per lot row, by the row's index. */
  refused: ReadonlyMap<number, string>;
}

/**
 * A tracked delivery line's lots (manufacturing phase 2 M77): the lots it is
 * taken from, FEFO-proposed when the delivery was made, each with its expiry,
 * location and quantity; *Change lot* picks among the site's lots of the
 * variant in FEFO order — a lot on hold cannot be chosen, nor an expired one
 * unless *Use expired lot* is ticked, which only a holder of
 * `inventory:expired-override` is offered. *Short by* shows what the lots do
 * not cover of the line; the post refuses until they add up.
 */
export const DeliveryLots = ({
  line,
  position,
  picks,
  quantity: lineQuantity,
  editable,
  canOverrideExpired,
  onChange,
  refused,
}: DeliveryLotsProps) => {
  const { t, quantity, date } = useOrdersFormat();
  const [useExpired, setUseExpired] = useState(false);
  const known = (pick: LotPickRow) => {
    const choice = line.lotChoices.find((c) => c.lotId === pick.lotId && c.locationId === pick.locationId);
    const stored = line.lots.find((l) => l.lotId === pick.lotId && l.locationId === pick.locationId);
    return {
      number: choice?.lotNumber ?? stored?.lotNumber ?? String(pick.lotId),
      expiry: choice ? choice.expiry : stored?.expiry,
      code: choice?.fullCode ?? stored?.locationCode ?? "",
      expired: choice?.expired ?? false,
      status: choice?.status,
    };
  };
  const total = lotsTotal(picks);
  const short = Math.round((lineQuantity - total) * 1000) / 1000;
  const taken = new Set(picks.map((pick) => `${pick.lotId}:${pick.locationId}`));
  const options = line.lotChoices
    .filter((choice) => !taken.has(`${choice.lotId}:${choice.locationId}`))
    .map((choice) => {
      const marks = [
        choice.expired ? t("expiredLot") : undefined,
        choice.status !== "available" ? t(`lotStatus.${choice.status}`) : undefined,
      ].filter(Boolean);
      return {
        value: `${choice.lotId}:${choice.locationId}`,
        label: [
          choice.lotNumber,
          choice.expiry ? date(choice.expiry) : "—",
          `${choice.fullCode} (${quantity(choice.quantity)})`,
          ...marks,
        ].join(" · "),
        disabled: choice.status !== "available" || (choice.expired && !useExpired),
      };
    });
  const choose = (value: string | null) => {
    const choice = line.lotChoices.find((c) => `${c.lotId}:${c.locationId}` === value);
    if (!choice) return;
    // Short: the lot covers what is missing; covered: it replaces the lots.
    const want = short > 0 ? short : lineQuantity;
    const pick: LotPickRow = {
      lotId: choice.lotId,
      locationId: choice.locationId,
      quantity: Math.min(want, choice.quantity),
      allowExpired: choice.expired,
    };
    onChange(short > 0 ? [...picks, pick] : [pick]);
  };
  const set = (index: number, patch: Partial<LotPickRow>) =>
    onChange(picks.map((pick, i) => (i === index ? { ...pick, ...patch } : pick)));

  return (
    <Stack gap={6} data-testid={`delivery-lots-${position}`}>
      {picks.length > 0 && (
        <Table verticalSpacing={4} withRowBorders={false}>
          <Table.Thead>
            <Table.Tr>
              <Table.Th>{t("lotColumn")}</Table.Th>
              <Table.Th>{t("expiryColumn")}</Table.Th>
              <Table.Th>{t("lotLocationColumn")}</Table.Th>
              <Table.Th w={140}>{t("quantity")}</Table.Th>
              {editable && <Table.Th w={40} />}
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {picks.map((pick, index) => {
              const lot = known(pick);
              const problem = refused.get(index);
              return (
                <Table.Tr key={`${pick.lotId}:${pick.locationId}`}>
                  <Table.Td>
                    <Group gap={6}>
                      <Text size="sm">{lot.number}</Text>
                      {(pick.allowExpired || lot.expired) && (
                        <Badge size="xs" color="red" variant="light">
                          {t("expiredLot")}
                        </Badge>
                      )}
                    </Group>
                    {problem && (
                      <Text size="xs" c="red" role="alert">
                        {problem}
                      </Text>
                    )}
                  </Table.Td>
                  <Table.Td>
                    <Text size="sm">{lot.expiry ? date(lot.expiry) : "—"}</Text>
                  </Table.Td>
                  <Table.Td>
                    <Text size="sm">{lot.code}</Text>
                  </Table.Td>
                  <Table.Td>
                    {editable ? (
                      <NumberInput
                        size="xs"
                        aria-label={t("lotQuantity", { lot: lot.number, position })}
                        min={0}
                        decimalScale={3}
                        value={pick.quantity}
                        onChange={(value) => set(index, { quantity: typeof value === "number" ? value : "" })}
                        rightSection={<Text size="xs">{line.unit}</Text>}
                      />
                    ) : (
                      <Text size="sm">
                        {quantity(pick.quantity === "" ? 0 : pick.quantity)} {line.unit}
                      </Text>
                    )}
                  </Table.Td>
                  {editable && (
                    <Table.Td>
                      <ActionIcon
                        variant="subtle"
                        color="red"
                        aria-label={t("removeLot", { lot: lot.number })}
                        onClick={() => onChange(picks.filter((_, i) => i !== index))}
                      >
                        <IconTrash size={14} />
                      </ActionIcon>
                    </Table.Td>
                  )}
                </Table.Tr>
              );
            })}
          </Table.Tbody>
        </Table>
      )}
      <Group gap="sm">
        {short > 0 && (
          <Badge color="orange" variant="light" data-testid="lots-short">
            {t("shortBy", { quantity: quantity(short) })}
          </Badge>
        )}
        {short < 0 && (
          <Badge color="orange" variant="light" data-testid="lots-over">
            {t("lotsOver", { total: quantity(total), quantity: quantity(lineQuantity) })}
          </Badge>
        )}
        {editable && (
          <Select
            size="xs"
            w={360}
            aria-label={t("changeLotOf", { position })}
            placeholder={options.length === 0 ? t("noLotsAtSite") : t("changeLot")}
            data={options}
            value={null}
            searchable
            onChange={choose}
          />
        )}
        {editable && canOverrideExpired && (
          <Checkbox
            size="xs"
            label={t("useExpiredLot")}
            checked={useExpired}
            onChange={(event) => setUseExpired(event.currentTarget.checked)}
          />
        )}
      </Group>
    </Stack>
  );
};
