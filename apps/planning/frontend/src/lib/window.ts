import { BOARD_DEFAULT_WEEKS } from "./search";
import { addDays, addWeeks, mondayOf, today } from "./weeks";

/** The project plan's window: from the given day's week, else this week, for twelve weeks (S11). */
export const planWindow = (search: { from?: string; to?: string }) => {
  const from = mondayOf(search.from ?? today());
  return { from, to: search.to ?? addDays(addWeeks(from, BOARD_DEFAULT_WEEKS), -1) };
};

/** The timeline's forward window: from today, else the given day, for twenty-six weeks (S12). */
export const timelineWindow = (search: { from?: string; to?: string }) => {
  const from = search.from ?? today();
  return { from, to: search.to ?? addWeeks(from, 26) };
};
