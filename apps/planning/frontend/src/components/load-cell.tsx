import { Box, Stack, Text, Tooltip, UnstyledButton } from "@mantine/core";
import type { PlanningWeekFigures } from "../api/board";
import { usePlanningFormat } from "../lib/format";
import { HATCH, loadBackgrounds, loadLevel } from "../lib/load";

export interface LoadCellProps {
  figures: PlanningWeekFigures;
  /** Who and which week, for the cell's accessible name. */
  label: string;
  /** Whether tentative hours are drawn (Show tentative). */
  showTentative: boolean;
  /** A planner's click: the allocation sheet for this week. */
  onClick?: () => void;
}

/**
 * One person's (or project's) week on the board: the load with its colour, a
 * hatched share for tentative hours, and red when a week with a day away is
 * booked past what the days left hold (`booked > available`, S7's
 * `booked_while_away`, amendment 37) — a conflict, never a silent re-plan
 * (S6). The tooltip
 * says the figures; never a type or a share of an absence, which the server
 * does not answer.
 */
export const LoadCell = ({ figures, label, showTentative, onClick }: LoadCellProps) => {
  const { t, hours, percent } = usePlanningFormat();
  const level = loadLevel(figures);
  const conflict = figures.away && figures.booked > figures.available;
  const tentative = showTentative && figures.tentative > 0;
  const tentativeShare =
    figures.capacity > 0 ? Math.min(100, (figures.tentative / figures.capacity) * 100) : tentative ? 100 : 0;
  const lines = [
    t("cellBooked", { hours: hours(figures.booked), capacity: hours(figures.capacity) }),
    ...(tentative ? [t("cellTentative", { hours: hours(figures.tentative) })] : []),
    t("cellFree", { hours: hours(figures.free) }),
    ...(figures.away ? [t(conflict ? "cellAwayConflict" : "cellAway")] : []),
  ];
  const body = (
    <Box
      data-level={level}
      data-conflict={conflict || undefined}
      px={6}
      py={4}
      style={{
        position: "relative",
        borderRadius: "var(--mantine-radius-sm)",
        background: conflict ? "var(--mantine-color-red-light)" : loadBackgrounds[level],
        border: conflict ? "1px solid var(--mantine-color-red-filled)" : undefined,
        minHeight: 40,
        overflow: "hidden",
      }}
    >
      {tentative && (
        <Box
          data-testid="tentative-share"
          aria-hidden
          style={{
            position: "absolute",
            left: 0,
            bottom: 0,
            height: 6,
            width: `${tentativeShare}%`,
            background: HATCH,
          }}
        />
      )}
      <Stack gap={0} style={{ position: "relative" }}>
        <Text size="sm" fw={600} c={conflict ? "red" : undefined}>
          {figures.loadPercent === undefined ? t("noCapacity") : percent(figures.loadPercent)}
        </Text>
        <Text size="xs" c="dimmed">
          {hours(figures.booked)}
          {tentative ? ` + ${hours(figures.tentative)}` : ""}
        </Text>
      </Stack>
    </Box>
  );
  const name = `${label}: ${lines.join(". ")}`;
  return (
    <Tooltip label={lines.join(" · ")} withArrow openDelay={300}>
      {onClick ? (
        <UnstyledButton aria-label={name} onClick={onClick} w="100%">
          {body}
        </UnstyledButton>
      ) : (
        <Box role="img" aria-label={name}>
          {body}
        </Box>
      )}
    </Tooltip>
  );
};
