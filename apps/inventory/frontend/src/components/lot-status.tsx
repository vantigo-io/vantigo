import { Anchor, Badge, Group, Text } from "@mantine/core";
import { Link } from "@tanstack/react-router";
import { type InventoryBalanceLot, type InventoryLotStatus, lotStatusColor } from "../api/lots";
import { useInventoryFormat } from "../lib/format";
import { lotLinkOptions } from "../lib/routes";
import { Quantity } from "./quantity";

/** A lot's status (manufacturing phase 2 M64) and whether it is past its expiry (M62), as badges; nothing for an available lot. */
export const LotStatusBadges = ({ status, expired }: { status: InventoryLotStatus; expired: boolean }) => {
  const { t } = useInventoryFormat();
  return (
    <>
      {status !== "available" && (
        <Badge size="xs" variant="light" color={lotStatusColor[status]}>
          {t(`lotStatus_${status}`)}
        </Badge>
      )}
      {expired && (
        <Badge size="xs" variant="light" color="red">
          {t("expired")}
        </Badge>
      )}
    </>
  );
};

/**
 * A location's lots under its stock row (M63): each lot's number, linked to
 * its page, its expiry, its status and what lies of it, in FEFO order as the
 * server answers them.
 */
export const LotLines = ({
  lots,
  unit,
  packagings,
}: {
  lots: readonly InventoryBalanceLot[];
  unit: string;
  packagings?: Parameters<typeof Quantity>[0]["packagings"];
}) => {
  const { t, day } = useInventoryFormat();
  return (
    <>
      {lots.map((lot) => (
        <Group key={lot.lotId} justify="space-between" wrap="nowrap" gap="xs" pl="md" data-lot={lot.number}>
          <Group gap={6} wrap="nowrap">
            <Anchor size="xs" ff="monospace" renderRoot={(props) => <Link {...lotLinkOptions(lot.lotId)} {...props} />}>
              {lot.number}
            </Anchor>
            {lot.expiry && (
              <Text size="xs" c={lot.expired ? "red" : "dimmed"}>
                {t("columnExpiry")} {day(lot.expiry)}
              </Text>
            )}
            <LotStatusBadges status={lot.status} expired={lot.expired} />
          </Group>
          <Text size="xs" component="div">
            <Quantity value={lot.onHand} unit={unit} packagings={packagings} />
          </Text>
        </Group>
      ))}
    </>
  );
};
