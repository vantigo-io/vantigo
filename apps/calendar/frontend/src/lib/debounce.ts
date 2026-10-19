/**
 * How long a picker waits after the last keystroke before it searches. The
 * tests run with none, so what they wait on is the stub's answer, not a timer.
 */
export const SEARCH_DEBOUNCE_MS = import.meta.env.MODE === "test" ? 0 : 250;
