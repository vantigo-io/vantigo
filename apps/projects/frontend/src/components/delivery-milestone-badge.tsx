import { Badge, type MantineSize } from "@mantine/core";
import { IconDiamond, IconDiamondFilled } from "@tabler/icons-react";
import type { DeliveryMilestoneSummary } from "../api/delivery-milestones";

export interface DeliveryMilestoneBadgeProps {
  milestone: DeliveryMilestoneSummary;
  size?: MantineSize;
  completed?: boolean;
}

/** A delivery milestone tag, marked with a diamond icon. */
export const DeliveryMilestoneBadge = ({ milestone, size = "sm", completed = false }: DeliveryMilestoneBadgeProps) => {
  return (
    <Badge
      variant="light"
      color={completed ? "teal" : "blue"}
      size={size}
      leftSection={completed ? <IconDiamondFilled size={12} /> : <IconDiamond size={12} />}
    >
      {milestone.name}
    </Badge>
  );
};
