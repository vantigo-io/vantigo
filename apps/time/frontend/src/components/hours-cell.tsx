import { TextInput, Tooltip } from "@mantine/core";
import { type KeyboardEvent, useRef, useState } from "react";
import { useHoursFormat } from "../lib/hours";
import { type TimeEntryStatus, timeEntryStatusColor } from "../lib/status";
import { parseHours } from "../lib/week";

export interface HoursCellProps {
  /** What a screen reader calls the cell: the row and the day. */
  label: string;
  /** The saved duration, or undefined for an empty day. */
  hours: number | undefined;
  /** The status of the entry the cell shows, which colours it. */
  status?: TimeEntryStatus;
  readOnly?: boolean;
  /** Said on hover: why the cell is read-only, or why its entry was rejected. */
  tooltip?: string;
  /** A new duration, or null when a saved one was cleared. Called only when the value changed. */
  onCommit: (hours: number | null) => void;
  /** The typed text is not a duration; the cell has already gone back to the saved value. */
  onInvalid: () => void;
  /** Where a read-only cell sends the person instead — the day view, for a day the grid cannot write. */
  onActivate?: () => void;
}

/**
 * One day of one row in the week grid. It takes the three ways people write
 * a duration (`7.5`, `7,5`, `7:30`) and saves on blur or Enter; Escape puts
 * the saved value back. Only a changed value is committed, so tabbing
 * through the grid saves nothing.
 */
export const HoursCell = ({
  label,
  hours,
  status,
  readOnly,
  tooltip,
  onCommit,
  onInvalid,
  onActivate,
}: HoursCellProps) => {
  const format = useHoursFormat();
  const saved = hours === undefined ? "" : format.input(hours);
  const [text, setText] = useState(saved);
  // The saved value moves when the week is read back after a write: follow
  // it, adjusting state during render as React documents, not in an effect.
  const [lastSaved, setLastSaved] = useState(saved);
  const cancelled = useRef(false);
  if (saved !== lastSaved) {
    setLastSaved(saved);
    setText(saved);
  }

  const commit = () => {
    // Escape blurs the input, so the blur that follows it must not save what
    // the person just cancelled.
    if (cancelled.current) {
      cancelled.current = false;
      setText(saved);
      return;
    }
    const value = text.trim();
    if (readOnly || value === saved) return;
    if (value === "") {
      if (hours !== undefined) onCommit(null);
      return;
    }
    const parsed = parseHours(value);
    if (parsed === undefined) {
      setText(saved);
      onInvalid();
      return;
    }
    if (parsed === hours) {
      setText(saved);
      return;
    }
    onCommit(parsed);
  };

  const onKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.key === "Enter") event.currentTarget.blur();
    if (event.key === "Escape") {
      cancelled.current = true;
      setText(saved);
      event.currentTarget.blur();
    }
  };

  const color = status ? timeEntryStatusColor(status) : undefined;
  return (
    <Tooltip label={tooltip} disabled={!tooltip} withArrow multiline maw={280}>
      <TextInput
        aria-label={label}
        size="xs"
        w={68}
        inputMode="decimal"
        autoComplete="off"
        value={text}
        readOnly={readOnly}
        data-status={status}
        onChange={(event) => setText(event.currentTarget.value)}
        onClick={readOnly ? onActivate : undefined}
        onBlur={commit}
        onKeyDown={onKeyDown}
        styles={{
          input: {
            textAlign: "right",
            ...(color && status !== "draft"
              ? {
                  borderColor: `var(--mantine-color-${color}-outline)`,
                  backgroundColor: `var(--mantine-color-${color}-light)`,
                }
              : {}),
            ...(readOnly ? { cursor: onActivate ? "pointer" : "default" } : {}),
          },
        }}
      />
    </Tooltip>
  );
};
