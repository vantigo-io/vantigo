import type { QueryClient } from "@tanstack/react-query";
import { act, waitFor } from "@testing-library/react";
import { expect } from "vitest";

/**
 * Lets a debounced search settle on real timers: waits past the customer
 * picker's debounce (250 ms) and for every read in flight to answer, round
 * after round, until a round asks for no new search. What a test then clicks
 * is the list the picker rests on — on a slow runner as on a fast one — not a
 * moment in its cycle. `searches` counts the searches asked for so far. The
 * fake-clock form of the same wait is `invoice.test.tsx`'s own.
 */
export const settleSearches = async (queryClient: QueryClient, searches: () => number): Promise<void> => {
  for (let round = 0; round < 8; round++) {
    const before = searches();
    await act(() => new Promise((resolve) => setTimeout(resolve, 400)));
    await waitFor(() => expect(queryClient.isFetching()).toBe(0));
    if (searches() === before) return;
  }
  throw new Error("the search never stopped asking");
};
