import { Badge, Tooltip } from "@mantine/core";
import { IconAlertTriangle } from "@tabler/icons-react";
import type { InventoryTransferStatus } from "../api/transfers";
import { transferStatusColor, useInventoryFormat } from "../lib/format";

/** A transfer's status as a badge (I36). */
export const TransferStatusBadge = ({ status }: { status: InventoryTransferStatus }) => {
  const { t } = useInventoryFormat();
  return (
    <Badge variant="light" color={transferStatusColor[status]}>
      {t(`transferStatus_${status}`)}
    </Badge>
  );
};

/** The overdue mark: sent or partly received and past its expected arrival, or a week after sending without one (I62). */
export const OverdueMark = () => {
  const { t } = useInventoryFormat();
  return (
    <Tooltip label={t("transferOverdueHint")}>
      <Badge
        variant="light"
        color="red"
        leftSection={<IconAlertTriangle size={12} />}
        aria-label={t("transferOverdue")}
      >
        {t("transferOverdue")}
      </Badge>
    </Tooltip>
  );
};
