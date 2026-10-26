import { Box, Text, UnstyledButton } from "@mantine/core";
import type { ReactNode } from "react";
import type { PlanningAllocation } from "../api/allocations";
import { usePlanningFormat } from "../lib/format";
import { HATCH } from "../lib/load";
import { addDays, mondayOf } from "../lib/weeks";

export interface Span {
  from: string;
  to: string;
}

/** The weeks a span covers on a strip of Mondays, as grid columns; undefined when it misses the strip. */
const columnsOf = (span: Span, weeks: readonly string[]): { start: number; end: number } | undefined => {
  if (weeks.length === 0) return undefined;
  const last = addDays(weeks[weeks.length - 1], 6);
  if (span.to < weeks[0] || span.from > last) return undefined;
  const start = weeks.indexOf(mondayOf(span.from < weeks[0] ? weeks[0] : span.from));
  const end = weeks.indexOf(mondayOf(span.to > last ? last : span.to));
  return { start, end };
};

/** One lane of spans over the weeks: employment, days away, or a person's or a project's allocations. */
export const SpanLane = ({
  label,
  weeks,
  spans,
}: {
  label: string;
  weeks: readonly string[];
  spans: { key: string; span: Span; color: string; title: string; content?: ReactNode }[];
}) => (
  <Box
    component="ul"
    aria-label={label}
    m={0}
    p={0}
    style={{
      listStyle: "none",
      display: "grid",
      gridTemplateColumns: `repeat(${weeks.length}, minmax(0, 1fr))`,
      gridAutoRows: 24,
      rowGap: 2,
      minHeight: 24,
    }}
  >
    {spans.map(({ key, span, color, title, content }) => {
      const columns = columnsOf(span, weeks);
      if (!columns) return null;
      return (
        <Box
          component="li"
          key={key}
          title={title}
          aria-label={title}
          px={6}
          style={{
            gridColumn: `${columns.start + 1} / ${columns.end + 2}`,
            background: color,
            borderRadius: "var(--mantine-radius-sm)",
            display: "flex",
            alignItems: "center",
            overflow: "hidden",
          }}
        >
          {content}
        </Box>
      );
    })}
  </Box>
);

export interface AllocationBarsProps {
  allocations: readonly PlanningAllocation[];
  /** The Mondays of the strip. */
  weeks: readonly string[];
  /** The lane's accessible name. */
  label: string;
  /** What each bar says first: the project on a person's row, the person on a project's. */
  name: (allocation: PlanningAllocation) => string;
  /** A planner's click on a bar: the allocation sheet in edit mode. */
  onOpen?: (allocation: PlanningAllocation) => void;
}

/**
 * Allocations as bars over the weeks (design §7): one row each, its weeks
 * filled, a tentative one hatched apart (S3), with the load and the title.
 */
export const AllocationBars = ({ allocations, weeks, label, name, onOpen }: AllocationBarsProps) => {
  const { t, load, range } = usePlanningFormat();
  return (
    <SpanLane
      label={label}
      weeks={weeks}
      spans={allocations.map((allocation) => {
        const text = [
          name(allocation),
          load(allocation.load, allocation.loadUnit),
          ...(allocation.title ? [allocation.title] : []),
        ].join(" · ");
        const title = `${text} · ${range(allocation.fromDate, allocation.toDate)} · ${t(`status_${allocation.status}`)}`;
        const tentative = allocation.status === "tentative";
        const inner = (
          <Text size="xs" fw={500} truncate>
            {text}
          </Text>
        );
        return {
          key: String(allocation.id),
          span: { from: allocation.fromDate, to: allocation.toDate },
          color: tentative ? HATCH : "var(--mantine-color-blue-light)",
          title,
          content: onOpen ? (
            <UnstyledButton
              data-status={allocation.status}
              aria-label={t("editAllocationNamed", { name: title })}
              onClick={() => onOpen(allocation)}
              w="100%"
            >
              {inner}
            </UnstyledButton>
          ) : (
            <Box data-status={allocation.status} w="100%">
              {inner}
            </Box>
          ),
        };
      })}
    />
  );
};
