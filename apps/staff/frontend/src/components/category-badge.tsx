import { Badge, Tooltip } from "@mantine/core";
import { useI18n } from "@vantigo/frontend-shell";
import type { StaffDocumentClass } from "../api/documents";
import "../i18n";

const colors: Record<StaffDocumentClass, string> = { A: "blue", D: "gray", E: "red" };

/**
 * A category's retention class (design S15) — A pay and contract, D
 * personnel administration, E health — as a short label, with the rule it
 * stands for in the tooltip.
 */
export const CategoryBadge = ({ documentClass }: { documentClass: StaffDocumentClass }) => {
  const { t } = useI18n("staff");
  return (
    <Tooltip label={t(`classExplained_${documentClass}`)} multiline w={280} withArrow>
      <Badge variant="light" color={colors[documentClass]} style={{ cursor: "help" }}>
        {t(`class_${documentClass}`)}
      </Badge>
    </Tooltip>
  );
};
