import { useQuery } from "@tanstack/react-query";
import { useCallback, useEffect, useState } from "react";
import { codeSuggestionQueryOptions } from "../api/projects";

/** How long the form waits after a keystroke before asking for a code. */
const SUGGESTION_DEBOUNCE_MS = 300;

export interface CodeSuggestion {
  /** The code the API suggests for the customer and name as they stand, or undefined while there is none. */
  suggestion: string | undefined;
  /** Whether the form's code field still tracks the suggestion. */
  isFollowing: boolean;
  /** What the user typed into the code field; anything but an empty field stops the tracking. */
  setManual: (value: string) => void;
  /** Tracks the suggestion again, from the form's "use suggestion" action. */
  useSuggestion: () => void;
}

/**
 * The code the API would give this project, and whether the form's code field
 * still follows it (design §4.2, §8.2). A create form starts following; an
 * edit form never does and never asks, because a code timesheets and billing
 * lines already quote must not drift as the name is corrected.
 *
 * Nothing is asked until the question has an answer: a name, and either a
 * customer or an internal project.
 */
export const useCodeSuggestion = (
  customerId: number | undefined,
  internal: boolean,
  name: string,
  enabled: boolean,
): CodeSuggestion => {
  const [isFollowing, setIsFollowing] = useState(enabled);
  // An internal project is derived from its own name alone, whatever customer
  // the picker held a moment ago.
  const target: number | "internal" | null = internal ? "internal" : (customerId ?? null);
  const trimmedName = name.trim();

  // Customer and name are debounced together: a customer picked while the
  // previous name is still settling must not ask a question about both halves
  // of two different answers.
  const [asked, setAsked] = useState({ target, name: trimmedName });
  useEffect(() => {
    const timeoutId = setTimeout(() => setAsked({ target, name: trimmedName }), SUGGESTION_DEBOUNCE_MS);
    return () => clearTimeout(timeoutId);
  }, [target, trimmedName]);

  const canSuggest = enabled && asked.name !== "" && asked.target !== null;
  const forCustomer = asked.target === "internal" || asked.target === null ? undefined : asked.target;

  const { data } = useQuery({
    ...codeSuggestionQueryOptions({ customerId: forCustomer, name: asked.name }),
    enabled: canSuggest,
  });

  const setManual = useCallback((value: string) => setIsFollowing(value.trim() === ""), []);
  const followAgain = useCallback(() => setIsFollowing(true), []);

  return {
    suggestion: canSuggest ? data?.code : undefined,
    isFollowing,
    setManual,
    useSuggestion: followAgain,
  };
};
