import { Tour } from "@mantine/core";
import {
  type AssistTarget,
  assistSelector,
  routeHasParams,
  routeMatches,
  runtimeBase,
  useI18n,
} from "@vantigo/frontend-shell";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { type ActiveTour, endTour, TOUR_TARGET_WAIT_MS, useActiveTour, waitForElement } from "../lib/tour";
import "../i18n";

export interface TourRunnerProps {
  /** Every module frontend's registered targets, collected by the host. */
  targets: readonly AssistTarget[];
  /** Opens an in-app route through the host's router (the panel stays open). */
  onNavigate: (route: string) => void;
  /** A tour begins; the host may close a full-screen panel that would cover the controls. */
  onStart?: () => void;
  /** The app-relative path on screen. Defaults to the browser's location without the app's base path. */
  currentPath?: () => string;
  /** How long a step waits for its control. */
  waitMs?: number;
}

const browserPath = () => {
  const base = runtimeBase().replace(/\/$/, "");
  const path = window.location.pathname;
  return base && path.startsWith(base) ? path.slice(base.length) || "/" : path;
};

/** Hands the focus back to the button that began the tour, or else into the panel. */
const returnFocus = (tour: ActiveTour) => {
  const button = tour.returnFocus;
  if (button?.isConnected) {
    button.focus();
    return;
  }
  document.querySelector<HTMLElement>("[data-assistant-panel] textarea")?.focus();
};

interface RunningTourProps extends TourRunnerProps {
  tour: ActiveTour;
}

/**
 * One tour being played. It is mounted afresh for each run, so its state starts
 * clean. A step is shown when its control is: on its own route, or after the
 * runner has opened that route, within `waitMs`; a control that never comes
 * (hidden by permissions, not loaded, a dialog the person did not open) shows
 * the step centred, with its text. While it waits, the tour is hidden, so a step
 * never points at nothing.
 *
 * The runner never clicks, fills in or submits anything. The overlay lets the
 * person's clicks through the spotlight, which is how a dialog the next step
 * points into gets opened — by them.
 */
const RunningTour = ({
  tour,
  targets,
  onNavigate,
  onStart,
  currentPath = browserPath,
  waitMs = TOUR_TARGET_WAIT_MS,
}: RunningTourProps) => {
  // The host's callbacks are new on every render; the tour must not restart for that.
  const hooks = useRef({ onNavigate, onStart, currentPath });
  useLayoutEffect(() => {
    hooks.current = { onNavigate, onStart, currentPath };
  });
  const { t } = useI18n("assistant");
  const byKey = useMemo(() => new Map(targets.map((target) => [target.key, target])), [targets]);
  const { steps } = tour;
  const [index, setIndex] = useState(0);
  const [ready, setReady] = useState(false);
  const [found, setFound] = useState<readonly boolean[]>([]);
  const latest = useRef(0);
  const abort = useRef<AbortController | null>(null);

  const onRoute = useCallback(
    (key: string) => {
      const target = byKey.get(key);
      return target !== undefined && routeMatches(target.route, hooks.current.currentPath());
    },
    [byKey],
  );

  /** Brings step i to screen: navigate if needed, wait for the control, then show it. */
  const resolve = useCallback(
    async (i: number) => {
      const mine = ++latest.current;
      abort.current?.abort();
      const controller = new AbortController();
      abort.current = controller;
      const target = byKey.get(steps[i].target);
      let present = false;
      if (target) {
        if (!routeMatches(target.route, hooks.current.currentPath()) && !routeHasParams(target.route))
          hooks.current.onNavigate(target.route);
        present = await waitForElement(assistSelector(target.key), waitMs, controller.signal);
      }
      if (latest.current !== mine) return;
      setFound((before) => {
        const next = [...before];
        next[i] = present;
        return next;
      });
      setIndex(i);
      setReady(true);
    },
    [byKey, steps, waitMs],
  );

  // The run begins: the first step. (Effects may run twice in development; the tour starts once.)
  const announced = useRef(false);
  useEffect(() => {
    if (!announced.current) {
      announced.current = true;
      hooks.current.onStart?.();
    }
    void resolve(0);
    return () => {
      latest.current += 1;
      abort.current?.abort();
    };
  }, [resolve]);

  // Gone: hand the focus back to the button, or into the panel.
  const focusTour = useRef(tour);
  useEffect(() => {
    const finished = focusTour.current;
    return () => returnFocus(finished);
  }, []);

  const go = (next: number) => {
    const target = byKey.get(steps[next]?.target ?? "");
    // Already there: no flicker through the waiting state.
    if (target && onRoute(target.key) && document.querySelector(assistSelector(target.key))) {
      latest.current += 1;
      setFound((before) => {
        const copy = [...before];
        copy[next] = true;
        return copy;
      });
      setIndex(next);
      return;
    }
    setReady(false);
    void resolve(next);
  };

  return (
    <Tour
      active={ready}
      step={index}
      onStepChange={go}
      onClose={endTour}
      withOverlayInteraction
      closeOnEscape
      withKeyboardNavigation
      labels={{
        skip: t("tourSkip"),
        back: t("tourBack"),
        next: t("tourNext"),
        close: t("tourClose"),
        stepCounter: (current, total) => t("tourStepCounter", { current, total }),
        beacon: t("tourBeacon"),
      }}
    >
      {steps.map((step, i) => (
        <Tour.Step
          key={i}
          target={found[i] && byKey.has(step.target) ? assistSelector(step.target) : undefined}
          title={step.title}
        >
          {step.body}
        </Tour.Step>
      ))}
    </Tour>
  );
};

/**
 * Plays the guided tour a "Show me" button started (design S11). Mount it once,
 * in the shell, beside the panel: it lives as long as the layout does, so a tour
 * goes on across routes and when the panel is closed.
 */
export const TourRunner = (props: TourRunnerProps) => {
  const tour = useActiveTour();
  return tour ? <RunningTour key={tour.run} tour={tour} {...props} /> : null;
};
