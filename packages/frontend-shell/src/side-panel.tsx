import { ActionIcon, Box, Group, Tooltip, UnstyledButton } from "@mantine/core";
import { useMediaQuery } from "@mantine/hooks";
import { IconLayoutRows, IconX } from "@tabler/icons-react";
import {
  createContext,
  Fragment,
  type ReactNode,
  type RefObject,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { useI18n } from "./i18n";

/**
 * One panel of the shell's right-hand side panel (Messenger design N24). A module
 * (the Assistant, Conversations) provides one; the host lists them and the shell
 * shows them one at a time or both at once.
 */
export type SidePanel = {
  /** Stable identity: "assistant", "conversations". */
  key: string;
  /** The tab's name, in the module's own language. */
  label: string;
  icon: ReactNode;
  /** A count or a status dot beside the label. */
  badge?: ReactNode;
  /** Listed but not shown now, e.g. the Assistant on its own page. */
  hidden?: boolean;
  /** The panel's content. Called while the panel is mounted; it is mounted the first time it is shown and then kept. */
  render: () => ReactNode;
};

export type SidePanelMode = "one" | "both";

export const SIDE_PANEL_MIN_WIDTH = 320;
export const SIDE_PANEL_MAX_WIDTH = 720;
export const SIDE_PANEL_DEFAULT_WIDTH = 400;
/** The least height either panel keeps in the stacked mode. */
export const SIDE_PANEL_MIN_PANE_HEIGHT = 200;
const DIVIDER_HEIGHT = 8;
const DEFAULT_RATIO = 0.5;

const WIDTH_KEY = "vantigo.sidePanel.width";
const LEGACY_WIDTH_KEY = "vantigo.assistant.panelWidth";
const MODE_KEY = "vantigo.sidePanel.mode";
const RATIO_KEY = "vantigo.sidePanel.ratio";
const LAST_KEY = "vantigo.sidePanel.last";

export const clampSidePanelWidth = (width: number) =>
  Math.min(SIDE_PANEL_MAX_WIDTH, Math.max(SIDE_PANEL_MIN_WIDTH, Math.round(width)));

/** The height of the upper panel in the stacked mode, so that both keep at least 200 px. */
export const clampSplit = (firstPx: number, totalPx: number) => {
  const max = totalPx - SIDE_PANEL_MIN_PANE_HEIGHT - DIVIDER_HEIGHT;
  if (max < SIDE_PANEL_MIN_PANE_HEIGHT) return Math.max(0, (totalPx - DIVIDER_HEIGHT) / 2);
  return Math.min(max, Math.max(SIDE_PANEL_MIN_PANE_HEIGHT, firstPx));
};

const read = (key: string): string | null => {
  try {
    return window.localStorage.getItem(key);
  } catch {
    return null;
  }
};
const write = (key: string, value: string) => {
  try {
    window.localStorage.setItem(key, value);
  } catch {
    /* the layout is a convenience; losing it is fine */
  }
};

const positive = (value: string | null) => {
  const n = Number(value);
  return value !== null && Number.isFinite(n) && n > 0 ? n : undefined;
};

/** The remembered width: the new key, else the Assistant's old one (read once, then written under the new key). */
export const readSidePanelWidth = (): number => {
  const stored = positive(read(WIDTH_KEY));
  if (stored !== undefined) return clampSidePanelWidth(stored);
  const legacy = positive(read(LEGACY_WIDTH_KEY));
  if (legacy !== undefined) {
    const migrated = clampSidePanelWidth(legacy);
    write(WIDTH_KEY, String(migrated));
    return migrated;
  }
  return SIDE_PANEL_DEFAULT_WIDTH;
};

const readRatio = () => {
  const stored = positive(read(RATIO_KEY));
  return stored !== undefined && stored >= 0.1 && stored <= 0.9 ? stored : DEFAULT_RATIO;
};

/**
 * The state: whether the panel is open (closed at the start, not remembered), the
 * panel in front, the mode, the width and the divider's ratio (the last three
 * remembered in this browser), and which panels have been mounted.
 */
const useSidePanelState = () => {
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState<string | undefined>(() => read(LAST_KEY) ?? undefined);
  const [mode, setModeState] = useState<SidePanelMode>(() => (read(MODE_KEY) === "both" ? "both" : "one"));
  const [ratio, setRatioState] = useState(readRatio);
  const [width, setWidthState] = useState(readSidePanelWidth);
  const [mounted, setMounted] = useState<ReadonlySet<string>>(() => new Set());
  // md: the stacked mode needs room; below it the mode is always one panel.
  const mdUp = useMediaQuery("(min-width: 62em)") === true;
  const visibleCount = useRef(0);

  const show = useCallback((key: string) => {
    setActive(key);
    write(LAST_KEY, key);
    setOpen(true);
  }, []);
  const close = useCallback(() => setOpen(false), []);
  const stacked = mode === "both" && mdUp && visibleCount.current >= 2;
  const toggle = useCallback(
    (key: string) => {
      if (open && (stacked || active === key)) setOpen(false);
      else show(key);
    },
    [open, stacked, active, show],
  );
  const setWidth = useCallback((next: number) => {
    const clamped = clampSidePanelWidth(next);
    setWidthState(clamped);
    write(WIDTH_KEY, String(clamped));
  }, []);
  const setMode = useCallback((next: SidePanelMode) => {
    setModeState(next);
    write(MODE_KEY, next);
  }, []);
  const setRatio = useCallback((next: number) => {
    const clamped = Math.min(0.9, Math.max(0.1, next));
    setRatioState(clamped);
    write(RATIO_KEY, String(clamped));
  }, []);
  const markMounted = useCallback((keys: readonly string[]) => {
    setMounted((current) => (keys.every((key) => current.has(key)) ? current : new Set([...current, ...keys])));
  }, []);

  return {
    open,
    active,
    mode,
    ratio,
    width,
    mounted,
    mdUp,
    show,
    close,
    toggle,
    setWidth,
    setMode,
    setRatio,
    markMounted,
    visibleCount,
  };
};

type HostController = ReturnType<typeof useSidePanelState>;

/**
 * The side panel's state, owned by the layout that persists across apps (see
 * `useSidePanelState`). Build the entries with their modules' hooks (they call
 * `show`/`toggle`), then give `aside(panels)` to `AppShellLayout`.
 */
export const useSidePanelController = () => {
  const controller = useSidePanelState();
  return {
    ...controller,
    /** For `AppShellLayout`'s `aside`; undefined while no module offers a panel. */
    aside: (panels: readonly SidePanel[]) => {
      controller.visibleCount.current = panels.filter((panel) => !panel.hidden).length;
      if (panels.length === 0) return undefined;
      return {
        opened: controller.open && controller.visibleCount.current > 0,
        width: controller.width,
        children: <SidePanelHost controller={controller} panels={panels} />,
      };
    },
  };
};

export type SidePanelController = ReturnType<typeof useSidePanelController>;

const ShownContext = createContext(true);
const ActionsContext = createContext<{ close: () => void; show: (key: string) => void } | undefined>(undefined);

/** Whether the panel being rendered is in front now: open, and shown (not on a hidden tab). Panels stop polling when it is false. */
export const useSidePanelShown = () => useContext(ShownContext);

/** Closes the side panel or brings another panel forward, from inside a panel. */
export const useSidePanelActions = () => {
  const actions = useContext(ActionsContext);
  return actions ?? { close: () => undefined, show: () => undefined };
};

/** The drag handle on the panel's left edge. Arrow keys resize it too. */
const WidthHandle = ({ width, onWidthChange }: { width: number; onWidthChange: (width: number) => void }) => {
  const { t } = useI18n("shell");
  const start = useRef<{ x: number; width: number } | undefined>(undefined);
  return (
    <Box
      role="separator"
      aria-orientation="vertical"
      aria-label={t("sidePanelResizeWidth")}
      aria-valuenow={width}
      aria-valuemin={SIDE_PANEL_MIN_WIDTH}
      aria-valuemax={SIDE_PANEL_MAX_WIDTH}
      tabIndex={0}
      visibleFrom="sm"
      style={{
        position: "absolute",
        left: -3,
        top: 0,
        bottom: 0,
        width: 6,
        cursor: "col-resize",
        zIndex: 2,
        touchAction: "none",
      }}
      onPointerDown={(event) => {
        start.current = { x: event.clientX, width };
        event.currentTarget.setPointerCapture?.(event.pointerId);
      }}
      onPointerMove={(event) => {
        if (start.current) onWidthChange(start.current.width + (start.current.x - event.clientX));
      }}
      onPointerUp={() => {
        start.current = undefined;
      }}
      onKeyDown={(event) => {
        if (event.key === "ArrowLeft") onWidthChange(width + 24);
        if (event.key === "ArrowRight") onWidthChange(width - 24);
      }}
    />
  );
};

/** The divider between the stacked panels: drag, or the arrow keys. */
const SplitHandle = ({
  ratio,
  onRatioChange,
  bodyRef,
  hidden,
}: {
  ratio: number;
  onRatioChange: (ratio: number) => void;
  bodyRef: RefObject<HTMLDivElement | null>;
  hidden: boolean;
}) => {
  const { t } = useI18n("shell");
  const dragging = useRef(false);
  const total = () => bodyRef.current?.getBoundingClientRect().height || 800;
  const setFirst = (px: number) => {
    const height = total();
    onRatioChange(clampSplit(px, height) / height);
  };
  return (
    <Box
      role="separator"
      aria-orientation="horizontal"
      aria-label={t("sidePanelResizeSplit")}
      aria-valuenow={Math.round(ratio * 100)}
      aria-valuemin={10}
      aria-valuemax={90}
      tabIndex={hidden ? -1 : 0}
      style={{
        display: hidden ? "none" : undefined,
        flex: `0 0 ${DIVIDER_HEIGHT}px`,
        cursor: "row-resize",
        touchAction: "none",
        background: "var(--mantine-color-default-border)",
      }}
      onPointerDown={(event) => {
        dragging.current = true;
        event.currentTarget.setPointerCapture?.(event.pointerId);
      }}
      onPointerMove={(event) => {
        const rect = bodyRef.current?.getBoundingClientRect();
        if (dragging.current && rect) setFirst(event.clientY - rect.top);
      }}
      onPointerUp={() => {
        dragging.current = false;
      }}
      onKeyDown={(event) => {
        const current = ratio * total();
        if (event.key === "ArrowUp") {
          event.preventDefault();
          setFirst(current - 24);
        }
        if (event.key === "ArrowDown") {
          event.preventDefault();
          setFirst(current + 24);
        }
      }}
    />
  );
};

export type SidePanelHostProps = { controller: HostController; panels: readonly SidePanel[] };

/**
 * The content of the layout's aside: a header with one tab per panel and the mode
 * toggle (when more than one panel is offered), and the panels. A panel is mounted
 * the first time it is shown and then stays mounted while another is in front, so
 * a half-written reply, a scroll position and an open conversation survive.
 */
export const SidePanelHost = ({ controller, panels }: SidePanelHostProps) => {
  const { t } = useI18n("shell");
  const { open, mode, mdUp, close, show, markMounted } = controller;
  const visible = panels.filter((panel) => !panel.hidden);
  const stacked = mode === "both" && mdUp && visible.length >= 2;
  const frontKey = visible.find((panel) => panel.key === controller.active)?.key ?? visible[0]?.key;
  const shownKeys = stacked ? visible.slice(0, 2).map((panel) => panel.key) : frontKey ? [frontKey] : [];
  const shownKey = shownKeys.join(",");
  const bodyRef = useRef<HTMLDivElement>(null);

  // Mount what is shown, and keep it mounted.
  useEffect(() => {
    if (open && shownKey) markMounted(shownKey.split(","));
  }, [open, shownKey, markMounted]);
  // The panel in front is not available here (the Assistant on its own page; Conversations in the inbox): close, as before.
  const activeHidden = panels.find((panel) => panel.key === controller.active)?.hidden === true;
  useEffect(() => {
    if (open && !stacked && (visible.length === 0 || activeHidden)) close();
  }, [open, stacked, visible.length, activeHidden, close]);

  const actions = useMemo(() => ({ close, show }), [close, show]);
  const mountedKeys = new Set(controller.mounted);
  if (open) for (const key of shownKeys) mountedKeys.add(key);

  return (
    <ActionsContext.Provider value={actions}>
      <Box
        data-side-panel
        // inert keeps a closed (slid-out) panel out of the tab order and the accessibility tree while it stays mounted
        inert={!open}
        h="100%"
        style={{ position: "relative", display: "flex", flexDirection: "column", minHeight: 0, width: "100%" }}
      >
        <WidthHandle width={controller.width} onWidthChange={controller.setWidth} />
        {visible.length > 1 && (
          <Group
            gap="xs"
            wrap="nowrap"
            px="xs"
            py={6}
            justify="space-between"
            style={{ borderBottom: "1px solid var(--mantine-color-default-border)" }}
          >
            <Group gap={4} wrap="nowrap" role="tablist" aria-label={t("sidePanels")} style={{ minWidth: 0 }}>
              {!stacked &&
                visible.map((panel) => (
                  <UnstyledButton
                    key={panel.key}
                    role="tab"
                    aria-selected={panel.key === frontKey}
                    onClick={() => show(panel.key)}
                    px="xs"
                    py={4}
                    style={{
                      display: "flex",
                      alignItems: "center",
                      gap: 6,
                      borderRadius: "var(--mantine-radius-sm)",
                      fontSize: "var(--mantine-font-size-sm)",
                      fontWeight: panel.key === frontKey ? 600 : 400,
                      background: panel.key === frontKey ? "var(--mantine-color-default-hover)" : undefined,
                    }}
                  >
                    {panel.icon}
                    {panel.label}
                    {panel.badge}
                  </UnstyledButton>
                ))}
            </Group>
            <Group gap={2} wrap="nowrap">
              <Tooltip label={mode === "both" ? t("sidePanelShowOne") : t("sidePanelShowBoth")}>
                <ActionIcon
                  variant={mode === "both" ? "light" : "subtle"}
                  color="gray"
                  visibleFrom="md"
                  aria-label={mode === "both" ? t("sidePanelShowOne") : t("sidePanelShowBoth")}
                  aria-pressed={mode === "both"}
                  onClick={() => controller.setMode(mode === "both" ? "one" : "both")}
                >
                  <IconLayoutRows size={18} />
                </ActionIcon>
              </Tooltip>
              <Tooltip label={t("sidePanelClose")}>
                <ActionIcon variant="subtle" color="gray" aria-label={t("sidePanelClose")} onClick={close}>
                  <IconX size={18} />
                </ActionIcon>
              </Tooltip>
            </Group>
          </Group>
        )}
        <Box ref={bodyRef} flex={1} style={{ minHeight: 0, display: "flex", flexDirection: "column" }}>
          {visible.map((panel, index) => {
            const shown = shownKeys.includes(panel.key);
            const first = stacked && index === 0;
            return (
              <Fragment key={panel.key}>
                {index === 1 && (
                  <SplitHandle
                    ratio={controller.ratio}
                    onRatioChange={controller.setRatio}
                    bodyRef={bodyRef}
                    hidden={!stacked}
                  />
                )}
                <Box
                  data-side-panel-pane={panel.key}
                  inert={!shown}
                  style={{
                    display: shown ? undefined : "none",
                    position: "relative",
                    overflow: "hidden",
                    minHeight: stacked ? SIDE_PANEL_MIN_PANE_HEIGHT : 0,
                    flex: first ? `0 1 ${controller.ratio * 100}%` : "1 1 0",
                  }}
                >
                  {mountedKeys.has(panel.key) && (
                    <ShownContext.Provider value={open && shown}>{panel.render()}</ShownContext.Provider>
                  )}
                </Box>
              </Fragment>
            );
          })}
        </Box>
      </Box>
    </ActionsContext.Provider>
  );
};
