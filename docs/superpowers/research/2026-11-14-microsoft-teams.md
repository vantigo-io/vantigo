# Microsoft Teams — research

Date: 2026-11-14. For the question of what Vantigo should do with Microsoft Teams next,
after the Entra module, the Outlook connection, the calendar's Outlook sync and the mail
channels. Read with [the Entra research](2026-10-19-entra-module.md) (§3.3 made a group a
team), [the calendar ↔ Outlook research](2026-10-27-calendar-outlook-sync.md) and
[the Outlook connection and mail research](2026-10-29-outlook-connection-and-mail.md).

It covers five candidates — a Teams link on an appointment, notifications in Teams,
Vantigo as an app inside Teams, Shifts, and reading Teams messages — and two small
neighbours, presence and automatic replies. Nothing here is decided; §9 recommends an
order and §10 lists what the owner must answer.

## 1. What exists today

- **The team itself.** `team: true` on one of Staff's Microsoft 365 groups makes it a
  team (`PUT /groups/{id}/team`, [Entra reference, *The team*](https://docs.vantigo.io/en/reference/entra/#the-team)),
  under `Group.ReadWrite.All`. The module never deletes a team, and does nothing inside
  one: no channel, no tab, no message.
- **The Outlook connection.** A per-person delegated token with `Calendars.ReadWrite`,
  through which the calendar writes each appointment as an event in the person's own
  mailbox and reads edits back by delta and change notifications. The event the module
  writes (`graphEvent`, `apps/server/internal/entra/mailbox.go`) carries the subject, a
  **text** body, a location, the times, `showAs`, the sensitivity and the marker —
  **no attendees and no online-meeting field**. The calendar mails its own invitations
  ([calendar invitations design](../specs/2026-10-19-calendar-invitations-design.md)),
  and a copy the person turns into a meeting in Outlook is **released** and never
  written again (calendar Outlook sync design O10.8).
- **The application.** Eight application permissions and the User Administrator role
  (Entra reference, *The required permissions*); anything else in the token is reported
  as `permissionsUnexpected`. Every permission below is therefore a new line in that
  table, a new consent by the customer's administrator, and a switch of its own.
- **Notifications.** There is no notification model in the host: the live design
  deferred the bell and the per-user topic to "a notifications design in the host"
  ([knowledge and live design](../specs/2026-11-07-knowledge-and-live-design.md), K37).
  What tells a person something today is mail.
- **Framing and the session.** The server answers `X-Frame-Options: DENY` and
  `frame-ancestors 'none'`, and the session cookie `vantigo.session` is `HttpOnly`,
  `SameSite=Strict` ([transport security](https://docs.vantigo.io/en/admin/transport-security/),
  [authentication](https://docs.vantigo.io/en/admin/authentication/)). Vantigo cannot be
  shown inside any other site today, by design.
- **Planning has no shifts.** Planning books a person on a project for a period at a
  load, in percent or hours a week; "Shifts and trading — rostering, open shifts and
  trades" is on its *Later* list
  ([planning reference, *What comes next*](https://docs.vantigo.io/en/reference/planning/#what-comes-next)).
- **Google.** The Google Workspace module mirrors the Outlook connection, the mailbox
  calendar and the mail channels. It has no Meet link either.

## 2. A Teams link on an appointment

Read on learn.microsoft.com on 2026-11-14.

- **Permission.** `POST /me/events` and `PATCH /me/events/{id}` with
  `isOnlineMeeting: true` and `onlineMeetingProvider: "teamsForBusiness"` need only
  `Calendars.ReadWrite`, which the Outlook connection already holds;
  `OnlineMeetings.ReadWrite` is not listed
  ([create event](https://learn.microsoft.com/en-us/graph/api/user-post-events?view=graph-rest-1.0),
  [online meetings on events](https://learn.microsoft.com/en-us/graph/outlook-calendar-online-meetings)).
  **No new consent.**
- **What comes back.** `onlineMeeting.joinUrl` and dial-in fields, and a join blob
  injected into the event's `body`. `onlineMeetingUrl` is to be deprecated.
- **It is one-way.** An existing event can be patched to become online, but "you cannot
  change the onlineMeetingProvider property, nor set isOnlineMeeting to false"
  ([update event](https://learn.microsoft.com/en-us/graph/api/event-update?view=graph-rest-1.0)).
  Taking the link off an appointment means deleting the event and making it again.
- **The body must be preserved.** An update of `body` on an online event has to read the
  event first and keep the blob; removing it "would disable meeting online". **This
  collides with the writer as it is**, which sends the whole text body on every update.
- **The provider must be allowed.** Check `allowedOnlineMeetingProviders` on the
  calendar. What Graph answers for a person with no Teams licence — an error or a silent
  `onlineMeeting: null` — is **not documented**.
- **An event with no attendees.** Every documented example has an attendee; no page says
  a link is refused without one, and none says it is issued. **Not confirmed** — and the
  module's copies never have attendees, so this is the first thing a tenant check must
  answer.
- **The alternative, a standalone meeting** (`POST /users/{id}/onlineMeetings`): delegated
  `OnlineMeetings.ReadWrite` — a new scope and a re-consent by every person — or
  application `OnlineMeetings.ReadWrite.All` plus an **application access policy** an
  administrator grants in PowerShell (`New-CsApplicationAccessPolicy`), up to thirty
  minutes to take effect
  ([application access policy](https://learn.microsoft.com/en-us/graph/cloud-communication-online-meeting-application-access-policy)).
  The meeting sits on no calendar. Limit: 2 000 meetings a user a month.

**What it means for Vantigo.** The link has to end up in two places: the organiser's
Outlook copy, and — the point of it — the invitation Vantigo mails to the customer's
contact. So the flow is: an appointment asks for a Teams meeting; the push creates or
patches the organiser's event with the two properties; the join address comes back and is
stored on the appointment; the invitation and the appointment's page carry it. The
organiser must have a live Outlook connection, and until the push has run the invitation
has no link — so either the invitation waits for the push, or the link is fetched in the
request. The Google side is the same shape (`conferenceData` on an event), and the
appointment's field should be provider-neutral: a meeting address and its provider.

## 3. Notifications in Teams

- **Activity feed notifications** are the supported way to tell one person something:
  `POST /users/{id}/teamwork/sendActivityNotification`, application permission
  `TeamsActivity.Send`, or the resource-specific `TeamsActivity.Send.User` granted at
  install ([send activity feed notifications](https://learn.microsoft.com/en-us/graph/teams-send-activityfeednotifications)).
  A bulk call takes a hundred users. 5 requests a second per application per tenant.
- **They need a Teams app.** "Teams app must be installed for the recipient", its
  manifest's `webApplicationInfo.id` must be the Entra application that sends, and
  templated notifications are declared under `activities.activityTypes`. Whether the
  free-text `systemDefault` type is generally available is contradicted between the Graph
  page and the Teams platform page.
- **They open Teams, not Vantigo.** With a text topic the `webUrl` "must start with the
  Microsoft Teams domain": it is a deep link into the app's tab. **So a notification is
  only useful once §4's tab exists** — it depends on that slice, and cannot ship before
  it.
- **Pre-installing for people** is possible app-only:
  `POST /users/{id}/teamwork/installedApps` with
  `TeamsAppInstallation.ReadWriteSelfForUser.All`, for an app already in the
  organisation's catalog; or the administrator pins it with an app setup policy.
- **Posting in a channel as the application is not available.** `POST
  /teams/{id}/channels/{id}/messages` takes application permission only for migration
  (`Teamwork.Migrate.All`), and the page adds that using Teams "as a log file" violates
  the terms of use
  ([send channel message](https://learn.microsoft.com/en-us/graph/api/channel-post-messages?view=graph-rest-1.0)).
- **Connectors are gone.** "Office 365 Connectors in Teams, including connector-based
  Incoming Webhooks, were retired in May 2026." The replacement is a webhook made in the
  **Workflows** app (Power Automate), for one channel or chat, taking an Adaptive Card;
  the flow belongs to the user who made it and is orphaned when they leave
  ([incoming webhooks](https://learn.microsoft.com/en-us/microsoftteams/platform/webhooks-and-connectors/how-to/add-incoming-webhook)).
- **A bot** can post proactively, once installed. It needs an Azure Bot resource in an
  Azure subscription, single-tenant since multi-tenant creation was deprecated after
  2025-07-31, and Microsoft's SDKs for single-tenant bots are C#, JavaScript and Python —
  no Go. As read, every installation would register its own bot in its own Azure
  subscription. Whether a bot can exist without the Azure resource is **not confirmed**.

**What it means for Vantigo.** Teams is a *delivery route*, not a notification model.
The model — what a notification is, who may see it, how it is read and cleared — is the
host design K37 is waiting for, and it comes first whatever Teams does. Once it exists,
two routes are cheap and bot-free: the activity feed for a person (through §4's app),
and a Workflows webhook address an administrator pastes for a channel ("new orders go to
#sales"), which is an outbound POST through the guarded client and nothing else.

## 4. Vantigo inside Teams

- **The package.** A zip of `manifest.json` (schema 1.30) and two icons; Teams hosts
  nothing. The domain and the client id are static values in it — `staticTabs[].contentUrl`,
  `validDomains`, `webApplicationInfo.id` and `.resource` — and "multiple domains per app
  aren't supported"
  ([app package](https://learn.microsoft.com/en-us/microsoftteams/platform/concepts/build-and-test/apps-package),
  [SSO manifest](https://learn.microsoft.com/en-us/microsoftteams/platform/tabs/how-to/authentication/tab-sso-manifest)).
  **Every installation needs its own package**, which Vantigo can generate from the
  Entra connection and its base address and offer as a download.
- **Getting it into the tenant is a person's job.** A Teams Administrator uploads it
  under *Manage apps*; it appears "after a few hours". The Graph upload
  (`POST /appCatalogs/teamsApps`) is **delegated only** — application permission "not
  supported" — so Vantigo cannot do it with its secret
  ([publish app](https://learn.microsoft.com/en-us/graph/api/teamsapp-publish?view=graph-rest-1.0)).
  No store listing, no Partner Center and no validation by Microsoft are needed for an
  organisation's own app; the manifest is still schema-checked (a privacy and a terms
  address, HTTPS, the icons).
- **A personal tab is the host in a frame.** The page must be HTTPS, must call
  `app.initialize()` from `@microsoft/teams-js` (major version 2) or it is not shown,
  and must allow being framed by Teams: `frame-ancestors` with `teams.microsoft.com`,
  `*.teams.microsoft.com`, `*.cloud.microsoft`, and `*.office.com`,
  `*.microsoft365.com` and the Outlook hosts if the tab should also show there
  ([tab requirements](https://learn.microsoft.com/en-us/microsoftteams/platform/tabs/how-to/tab-requirements)).
  Sign-in pages do not render in a frame.
- **The cookie is the hard part.** In a frame Vantigo's cookie is third-party: it must be
  `SameSite=None; Secure`, and browsers are partitioning or dropping such cookies
  ([SameSite cookie update](https://learn.microsoft.com/en-us/microsoftteams/platform/resources/samesite-cookie-update)).
  Vantigo's is `SameSite=Strict` on purpose, as its second layer against request forgery.
  Microsoft's own pages disagree on what the desktop client accepts, and say nothing
  certain about WebView2 or iOS.
- **Single sign-on.** The Entra application exposes an API (`api://{domain}/{client-id}`,
  scope `access_as_user`) and pre-authorises Teams' client ids; the tab calls
  `authentication.getAuthToken()` and gets a token whose audience is the application,
  with `oid` and `tid`
  ([register for tab SSO](https://learn.microsoft.com/en-us/microsoftteams/platform/tabs/how-to/authentication/tab-sso-register-aad)).
  Single-tenant applications are supported and the person is not asked to consent. The
  newer nested app authentication does the same with MSAL.js and no exposed API; its
  page carries no preview label.
- **Deep links** into the tab —
  `https://teams.microsoft.com/l/entity/{appId}/{entityId}?context={"subEntityId":…}` —
  are what a notification opens, read by the tab as `page.subPageId`. The `appId` is the
  catalog's, not the manifest's, once uploaded.
- **Link previews need a bot.** Unfurling a pasted Vantigo address into a card, and a
  message extension that searches Vantigo, both go through a bot's messaging endpoint —
  §3's Azure Bot per installation. The bot-less *API-based* message extension supports
  search commands only, no previews
  ([link unfurling](https://learn.microsoft.com/en-us/microsoftteams/platform/messaging-extensions/how-to/link-unfurling),
  [API-based message extensions](https://learn.microsoft.com/en-us/microsoftteams/platform/messaging-extensions/api-based-overview)).
- **The same package** shows the personal tab in Outlook and the Microsoft 365 app.
  Whether an organisation's own app shows on Teams mobile without store approval is
  **not confirmed**.

**What it means for Vantigo.** The honest shape of the tab is a **second session mode**:
a session opened from a validated Teams token, held by the frame in memory and sent as a
header, never as a cookie — so the cookie stays `Strict`, request forgery stays
impossible for it, and the framing headers are relaxed only for a route that serves that
mode, and only while a switch is on. That is identity's work (a token validator, a
session kind, the header transport every module client must carry — see the session
handlers every client already shares), transport security's (the headers per route), the
host's (TeamsJS, no chrome duplicated, links that stay inside), and an operator
procedure in two languages. It is a real delivery, comparable to the Outlook connection,
not a wrapper. Link previews and search-from-compose are a second, larger one, and bring
Azure into every customer's setup.

## 5. Shifts

- **All of it is in Graph v1.0**: a schedule per team (`PUT /teams/{id}/schedule`,
  provisioned in the background), scheduling groups, shifts with a draft and a shared
  version, open shifts, time off and its reasons, swap, offer and time-off requests, and
  time cards with clock in and out
  ([schedule](https://learn.microsoft.com/en-us/graph/api/resources/schedule?view=graph-rest-1.0),
  [timeCard](https://learn.microsoft.com/en-us/graph/api/resources/timecard?view=graph-rest-1.0)).
- **Permission.** `Schedule.ReadWrite.All` (application). `Group.ReadWrite.All` does not
  cover it app-only. The `MS-APP-ACTS-AS` header is deprecated and no longer needed.
- **A write to `sharedShift` is seen at once** and notifies the worker in Teams;
  `draftShift` waits for a share. A shift is at most 24 hours.
- **Reading back is poor.** No delta query. Change notifications exist for shifts and the
  request types, not for time off or open shifts; polling a Teams resource is allowed
  "only once per day". 30 requests a second per application per tenant.
- **Workforce integrations are the mechanism for staying the authority.** Shifts calls
  the outside system synchronously on every change — the integration's own included —
  and a refusal in the answer denies it; denying everything makes Shifts read-only. The
  callback is AES-256-CBC with HMAC-SHA-256 over a Bond envelope, the secret exactly 64
  characters, and Vantigo's address must be reachable from Microsoft
  ([custom workforce integration](https://learn.microsoft.com/en-us/microsoft-365/frontline/shifts-custom-wfm-integration)).
- **Limits.** Users need a Teams licence (which plans include Shifts is not stated on the
  pages read); guests cannot be on a schedule; a schedule belongs to one team.

**What it means for Vantigo.** There is nothing to synchronise yet: Planning's rows are
allocations on projects, not shifts on days, and rostering is its own unbuilt phase. A
Shifts integration is a mirror of **Planning's shifts**, when Planning has them — and it
should then be designed with that phase, so the model (a shift, a group, an open shift,
a trade) is one Shifts can carry. Two smaller mirrors would work sooner but are thin:
Staff's approved absence as time off in a team's schedule, and Time's entries from time
cards. Neither is worth a new tenant-wide permission on its own.

## 6. Reading Teams messages

The cost argument against it is gone: "Starting August 25, 2025, the Teams APIs are no
longer metered"
([metered APIs](https://learn.microsoft.com/en-us/graph/metered-api-list)), and the
licensing-model page was removed. What remains is that it takes `Chat.Read.All` or
`ChannelMessage.Read.All` — every conversation in the tenant — an encryption certificate
for the notifications, and that Teams chat is colleagues talking, not customers.
Communications is for customers; nothing in Vantigo wants colleagues' chats.

## 7. Presence and automatic replies

- **Presence** can be read app-only (`Presence.Read.All`, 650 people a call) and set
  (`Presence.ReadWrite.All`, a session of at most four hours). Showing who is free on
  Planning's board or a customer's page is pleasant and tenant-wide; setting it from
  Vantigo has no clear use.
- **Automatic replies**: `PATCH /users/{id}/mailboxSettings` with
  `automaticRepliesSetting` — `scheduled`, a start and an end, an internal and an
  external message — under `MailboxSettings.ReadWrite`, delegated or application, for "a
  future date range" only
  ([update mailbox settings](https://learn.microsoft.com/en-us/graph/api/user-update-mailboxsettings?view=graph-rest-1.0)).
  An approved absence in Staff could set it. Delegated means a new scope on the Outlook
  connection and every person connecting again; application means a tenant-wide mailbox
  permission the module today reports as unexpected. Not Teams, but the smallest thing in
  this document a person would notice.

## 8. What each costs the customer's administrator

| Candidate | New permission | New setup step |
| --- | --- | --- |
| Teams link on an appointment | none | none |
| Channel webhook | none | paste a Workflows address per channel |
| Personal tab with sign-on | none in Graph; an exposed API on the application | upload the package, pin it |
| Activity feed notification | `TeamsActivity.Send` (or resource-specific at install) | the tab's, plus install for people |
| Link previews, search from compose | bot identity | an Azure Bot resource in their subscription |
| Shifts | `Schedule.ReadWrite.All` | a workforce integration, a reachable address |
| Automatic replies | `MailboxSettings.ReadWrite` | re-consent by each person, or tenant-wide |

## 9. Recommendations

1. **The Teams link first.** No consent, no setup, and it finishes something the calendar
   already does. Before a design: a tenant check of the three unknowns in §2 — a link on
   an event with no attendees, the answer for a person without a licence, and the body
   after an update. The design then settles the appointment's provider-neutral field, the
   body rule, the invitation's timing and Google Meet beside it.
2. **The host's notification design next**, on its own merits (K37), with delivery
   routes as a seam: mail today, the channel webhook as the first Teams route.
3. **The tab third**, as the session mode of §4, and the activity feed with it. This is
   the slice a customer can be sold on — "Vantigo in Teams" — and the one that changes
   the security posture, so it wants its own research into the cookie and the clients,
   a threat model and the owner's yes before a design.
4. **Shifts with Planning's rostering phase**, not before.
5. **Not now:** a bot (previews, search), reading messages, presence.
6. **Automatic replies** are worth a small design whenever Staff's absence next changes.

## 10. Questions for the owner

1. Is a Teams link one-way enough — once on an appointment, removed only by making the
   event again?
2. Should the link wait for Google Meet, or ship for Microsoft first with the field
   shaped for both?
3. Is relaxing the framing headers for a Teams route, behind a switch, acceptable at all?
   If not, the tab and the activity feed both fall, and Teams is the link and the channel
   webhook.
4. Is an Azure subscription an acceptable requirement for any feature? If not, link
   previews are out for good, not for now.
5. Does rostering in Planning move up because of Shifts, or does Shifts wait for it?

## 11. Not confirmed

- A join address on an event with no attendees; Graph's answer without a Teams licence.
- Whether `systemDefault` activity notifications are generally available.
- Third-party cookies in the Teams desktop client (WebView2) and on iOS — moot if the
  tab's session uses no cookie.
- An organisation's own app on Teams mobile without store approval.
- A bot without an Azure Bot resource; whether its endpoint must be publicly reachable.
- Which Microsoft 365 plans include Shifts; how a deleted shift shows in a later list.
- Whether nested app authentication is generally available on every client (stated by a
  Microsoft blog post seen only as a search result).
