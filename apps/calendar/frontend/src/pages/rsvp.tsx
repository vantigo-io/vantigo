import { Alert, Button, Card, Center, Group, Loader, Stack, Text, Title } from "@mantine/core";
import { IconAlertCircle, IconCircleCheck } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  browserTimeZone,
  civilDateIn,
  createLocaleFormatters,
  i18n,
  type SupportedLocale,
  useLocale,
} from "@vantigo/frontend-shell";
import { useEffect, useRef, useState } from "react";
import { CALENDAR_QUERY_KEY } from "../api/request";
import { answerRsvp, type RsvpAnswer, type RsvpPageView, readRsvpPage } from "../api/rsvp";
import "../i18n";
import { civilDateValue } from "../lib/dates";

/** Where the tab keeps the RSVP link's token once it has left the address bar. */
export const RSVP_STORAGE_KEY = "vantigo.calendar.rsvp";

const RSVP_QUERY_KEY = [CALENDAR_QUERY_KEY, "rsvp"] as const;

const storage = (): Storage | undefined => {
  try {
    return window.sessionStorage;
  } catch {
    return undefined;
  }
};

/**
 * The token the page's address names, read once and kept in the tab's
 * storage, so a reload still knows it once the address bar no longer does.
 */
const readToken = (): string | undefined => {
  const token = new URLSearchParams(window.location.search).get("token") || undefined;
  try {
    if (token) storage()?.setItem(RSVP_STORAGE_KEY, token);
    else return storage()?.getItem(RSVP_STORAGE_KEY) || undefined;
  } catch {
    // The page works without storage: the token is held in the page's state.
  }
  return token;
};

/** Takes the token out of the address bar, so history and screenshots never hold it. */
const clearAddressBar = () => {
  const url = new URL(window.location.href);
  if (!url.searchParams.has("token")) return;
  url.searchParams.delete("token");
  window.history.replaceState(window.history.state, "", `${url.pathname}${url.search}${url.hash}`);
};

const statusOf = (error: unknown) => (error as { status?: unknown } | null)?.status;

const answers: readonly { status: RsvpAnswer; key: string; color: string }[] = [
  { status: "accepted", key: "rsvp.accept", color: "teal" },
  { status: "tentative", key: "rsvp.tentative", color: "yellow" },
  { status: "declined", key: "rsvp.decline", color: "red" },
];

const statusKeys: Record<string, string> = {
  "needs-action": "statusNeedsAction",
  accepted: "statusAccepted",
  tentative: "statusTentative",
  declined: "statusDeclined",
};

type Translate = (key: string, options?: Record<string, unknown>) => string;

const time = { hour: "2-digit", minute: "2-digit", hour12: false } as const;

/** A timed span as one zone sees it: the day once when it starts and ends on the same one. */
const timedIn = (page: RsvpPageView, zone: string, language: SupportedLocale, t: Translate, withDate = true) => {
  const { formatDate } = createLocaleFormatters(language, zone);
  const start = page.startsAt as string;
  const end = page.endsAt as string;
  const date = (at: string) => formatDate(at, { dateStyle: "full" });
  if (civilDateIn(zone, new Date(start)) === civilDateIn(zone, new Date(end))) {
    const times = { start: formatDate(start, time), end: formatDate(end, time) };
    return withDate ? t("rsvp.sameDay", { date: date(start), ...times }) : t("rsvp.span", times);
  }
  return t("rsvp.span", {
    start: t("rsvp.dateTime", { date: date(start), time: formatDate(start, time) }),
    end: t("rsvp.dateTime", { date: date(end), time: formatDate(end, time) }),
  });
};

/**
 * When, as the invitee reads it (design D7): in the appointment's own zone,
 * and — for a timed one — in the browser's too when that is another; an
 * all-day one is its days, the same everywhere.
 */
const When = ({ page, language, t }: { page: RsvpPageView; language: SupportedLocale; t: Translate }) => {
  if (page.allDay) {
    const { formatDate } = createLocaleFormatters(language, "UTC");
    const startDate = page.startDate as string;
    const endDate = page.endDate ?? startDate;
    const day = (date: string, style: "full" | "medium") =>
      formatDate(civilDateValue(date), { dateStyle: style, timeZone: "UTC" });
    return (
      <Text>
        {startDate === endDate
          ? day(startDate, "full")
          : t("rsvp.span", { start: day(startDate, "medium"), end: day(endDate, "medium") })}
      </Text>
    );
  }
  const browser = browserTimeZone();
  const sameDayHere =
    civilDateIn(browser, new Date(page.startsAt as string)) ===
    civilDateIn(page.timeZone, new Date(page.startsAt as string));
  return (
    <Stack gap={0}>
      <Text>{t("rsvp.inZone", { when: timedIn(page, page.timeZone, language, t), zone: page.timeZone })}</Text>
      {browser !== page.timeZone && (
        <Text size="sm" c="dimmed">
          {t("rsvp.inBrowserZone", { when: timedIn(page, browser, language, t, !sameDayHere), zone: browser })}
        </Text>
      )}
    </Stack>
  );
};

export interface RsvpPageProps {
  /**
   * Takes the token out of the address bar once it is read — the host's
   * router, so its own location agrees. Without it the page replaces the
   * browser's address itself, keeping `history.state`.
   */
  clearAddress?: () => void;
}

/**
 * The public RSVP page (calendar invitations design D5, D7), at `/rsvp`
 * without a session: the appointment an invitation's link names — the title,
 * when, where, who organises it and for whom the invitation is — and the
 * three answers, the current one marked. The token is read from the address
 * once, kept in the tab's storage, taken out of the address bar and only ever
 * posted. The page speaks the language the invitation was written in
 * (`RsvpPage.language`), not the browser's.
 */
export const RsvpPage = ({ clearAddress }: RsvpPageProps) => {
  const browserLanguage = useLocale();
  const queryClient = useQueryClient();
  const [token] = useState(readToken);
  const [answered, setAnswered] = useState(false);
  // Once, on the address the page was opened at.
  const clear = useRef(clearAddress ?? clearAddressBar);
  useEffect(() => clear.current(), []);
  const read = useQuery({
    queryKey: RSVP_QUERY_KEY,
    queryFn: () => readRsvpPage(token ?? ""),
    enabled: !!token,
    retry: false,
    refetchOnWindowFocus: false,
  });
  const answer = useMutation({
    mutationFn: (status: RsvpAnswer) => answerRsvp(token ?? "", status),
    onSuccess: (page) => {
      queryClient.setQueryData(RSVP_QUERY_KEY, page);
      setAnswered(true);
    },
    // Cancelled since it was read: read it again, and the page says so.
    onError: (error) => {
      if (statusOf(error) === 409 || statusOf(error) === 404)
        void queryClient.invalidateQueries({ queryKey: RSVP_QUERY_KEY });
    },
  });
  const page = read.data;
  // Until the page answers there is no invitation's language to speak: the browser's, for the link's own failures.
  const language: SupportedLocale = page?.language ?? browserLanguage;
  const t = i18n.getFixedT(language, "calendar") as unknown as Translate;
  const invalid = !token || (read.isError && statusOf(read.error) === 404);
  const refusedAnswer = answer.isError && statusOf(answer.error) !== 409 && statusOf(answer.error) !== 404;
  return (
    <Center mih="100vh" p="md" bg="gray.0">
      <Stack w="100%" maw={480} gap="md" component="main" lang={language}>
        <Card withBorder shadow="sm" padding="lg" radius="md">
          <Stack gap="md">
            {invalid && (
              <Alert color="red" icon={<IconAlertCircle size={16} />} role="alert">
                {t("rsvp.invalid")}
              </Alert>
            )}
            {!invalid && read.isPending && (
              <Center>
                <Loader size="sm" />
              </Center>
            )}
            {!invalid && read.isError && (
              <Alert color="red" icon={<IconAlertCircle size={16} />} role="alert">
                {t("rsvp.couldNotLoad")}
              </Alert>
            )}
            {page && (
              <>
                <Title order={1} size="h2">
                  {page.title}
                </Title>
                <When page={page} language={language} t={t} />
                {page.location && <Text>{t("rsvp.where", { location: page.location })}</Text>}
                <Text size="sm" c="dimmed">
                  {page.company
                    ? t("rsvp.organisedByAt", { name: page.organiser.name, company: page.company })
                    : t("rsvp.organisedBy", { name: page.organiser.name })}
                </Text>
                <Text size="sm" c="dimmed">
                  {t("rsvp.invitationFor", { name: page.attendee.name })}
                </Text>
              </>
            )}
            {page?.cancelled && (
              <Alert color="orange" icon={<IconAlertCircle size={16} />} role="status">
                {t("rsvp.cancelled")}
              </Alert>
            )}
            {page && !page.cancelled && (
              <Stack gap="sm">
                {answered && (
                  <Alert color="green" icon={<IconCircleCheck size={16} />} role="status">
                    {t("rsvp.thanks")}
                  </Alert>
                )}
                {page.status !== "needs-action" ? (
                  <Text fw={500}>{t("rsvp.yourAnswer", { status: t(statusKeys[page.status]) })}</Text>
                ) : (
                  <Text fw={500}>{t("rsvp.question")}</Text>
                )}
                <Group gap="xs" grow>
                  {answers.map(({ status, key, color }) => (
                    <Button
                      key={status}
                      color={color}
                      variant={page.status === status ? "filled" : "light"}
                      aria-pressed={page.status === status}
                      loading={answer.isPending && answer.variables === status}
                      disabled={answer.isPending}
                      onClick={() => answer.mutate(status)}
                    >
                      {t(key)}
                    </Button>
                  ))}
                </Group>
                {refusedAnswer && (
                  <Alert color="red" icon={<IconAlertCircle size={16} />} role="alert">
                    {t("rsvp.couldNotAnswer")}
                  </Alert>
                )}
              </Stack>
            )}
          </Stack>
        </Card>
      </Stack>
    </Center>
  );
};
