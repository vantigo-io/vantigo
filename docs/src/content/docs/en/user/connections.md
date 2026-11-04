---
title: Connections
description: Connecting your own Outlook or Google account to Vantigo under Settings → Connections — who sees each card, what the sign-in to Microsoft or Google looks like, Connected as, the features, disconnecting, Reconnect needed and its reasons, and what each message when a connect fails means.
sidebar:
  order: 2
sources:
  - apps/host/frontend/src/routes/settings/connections.tsx
  - apps/host/frontend/src/lib/connections.tsx
  - apps/host/frontend/src/components/connection-cards.tsx
  - apps/host/frontend/src/components/entra-connection-card.tsx
  - apps/entra/frontend/src/components/outlook-connection.tsx
  - apps/google/frontend/src/components/google-connection.tsx
---

**Settings → Connections** lists the accounts elsewhere that you connect to Vantigo, one
card for each provider your organisation runs: your **Outlook**, from the Microsoft Entra
ID module, and your **Google** account, from the Google Workspace module
([Connect your Google account](#connect-your-google-account)). For Outlook, you connect it once, signing in to Microsoft, and Vantigo
keeps the connection until you disconnect it, leave, or your administrator turns it
off. The calendar's [Outlook sync](/en/user/calendar/#sync-with-outlook) uses it: Vantigo
writes your calendar's copies to your mailbox, and reads your busy time back, with your
own connection and nothing else. Communications also uses it to let you send messages from
your own address ([Send from your own address](/en/user/communications/#personal-sending-and-retrying)),
with customer replies directed to the shared channel.

## Who sees it

**Connections** is in the avatar menu's **Settings**, beside **Profile** and
**Security**, while your organisation runs at least one module that offers a connection —
the Microsoft Entra ID module or the Google Workspace module; without one the page does
not exist. The page shows
one card for each such module that is on, in the same order every time. Everyone signed in
sees their own connections and nobody else's; no permission is needed.

The **Outlook** card offers a connection only when two things hold:

- an Owner has turned on the **Outlook connection** on the Microsoft Entra ID page
  ([Microsoft Entra ID](/en/user/entra/#outlook-connection)); and
- HR has connected your person in Staff to a Microsoft account that Vantigo manages —
  one it created, or whose adoption someone confirmed. Vantigo never guesses your
  account from your address.

Otherwise the **Outlook** card is not shown. When no provider's card has anything to
offer you, the page says *Nothing to connect: no connection is turned on for you, or
Vantigo knows no account of yours to connect.*

## Connect your Outlook

1. Open **Settings → Connections**. The **Outlook** card says **Not connected**.
2. Choose **Connect Outlook**. Your browser goes to Microsoft's sign-in, with your work
   account filled in. If you are already signed in to Microsoft 365, it usually comes
   straight back without showing a page; otherwise sign in as you normally do, with
   multi-factor authentication if your organisation asks for it.
3. Back in Vantigo, the card says *Finishing connecting Outlook…*, then **Outlook
   connected**, and returns you to where you started.

You can also connect from the **Outlook** card under **Calendar → My calendar**, or
directly from the composer in **Communications → Inbox**, which show the same button —
one for each provider that offers you a connection — and bring you back there.

Sign in with **the account Vantigo knows you by**: the one HR connected to your person.
Signing in as anyone else — a second account, a personal Microsoft account, an account
from another organisation — is refused, and nothing is connected.

## Connected

The card says **Connected as** and your account, **Since** the day you connected, and
**Used by:** the features the connection serves, such as *Calendar* and *Mail*.

## Disconnect

Choose **Disconnect**, then **Disconnect** in *Disconnect Outlook?*. Vantigo stops using
your Outlook until you connect it again, and the card says **Not connected**.
Disconnecting does not sign you out of Microsoft or of Vantigo. **Vantigo removes what
the calendar's Outlook sync put in your Outlook**, with the connection you just closed,
and the sync waits until you connect again; its own switch stays as you left it.

## Reconnect needed

Microsoft can refuse a connection later — your sign-in expired, your password changed,
your organisation's rules changed. The card then says **Reconnect needed** with the
reason, and the button **Reconnect Outlook** connects again in the same way:

| The card says | Why |
| --- | --- |
| *Your sign-in to Microsoft has expired.* | The connection's sign-in at Microsoft expired — unused too long, or ended by Microsoft. |
| *Microsoft asks you to sign in again.* | Your password was changed, your sessions were revoked, or Microsoft wants a new sign-in for another reason. |
| *Microsoft asks you to complete multi-factor authentication.* | Your organisation now asks for a second factor. |
| *Your organisation's Conditional Access blocked the connection. Ask your administrator if it happens again.* | A Conditional Access policy refused it — a device, a place or a risk rule. |
| *Your organisation has not consented to a permission the connection needs. Ask your administrator.* | Admin consent for the connection's permissions is missing. |

Only you see this; it is not your administrator's failure. **The calendar's Outlook sync
pauses** until you reconnect — what it put in Outlook stays as it was — and picks up
where it left off once you have; nothing else in Vantigo stops. Your administrator sees how many people wait to reconnect on the Microsoft Entra
ID page.

If you leave, or your Microsoft account is disabled or deleted, the connection is closed
for you. What the calendar's Outlook sync put in your Outlook then stays in your mailbox,
which is disabled with your account.

## When connecting fails

When a connect cannot complete, the card says **Outlook could not be connected** and
why:

| The card says | Do |
| --- | --- |
| *The sign-in to Microsoft was cancelled or refused. Nothing was connected.* | Connect again, and accept on Microsoft's page. |
| *You signed in with an account from another organisation. Connect again and sign in with your work account.* | Sign out of the other account at Microsoft, or choose your work account. |
| *The connect took too long, or was started in another session. Connect again.* | Connect again, and finish within ten minutes in the same browser and the same tab: starting a new connect cancels one not yet finished. |
| *Microsoft's answer could not be used. Try again in a moment.* | Try again; if it keeps happening, tell your administrator. |
| *Your organisation has not consented to the permissions the connection needs. Ask your administrator to grant admin consent.* | Your administrator grants admin consent ([the administration guide](/en/admin/entra/#outlook-connection)). |
| *You signed in to Microsoft with another account than the one Vantigo knows you by. Connect again and sign in with that account.* | Sign in with your work account; ask HR if Vantigo knows you by the wrong one. |
| *Another person in Vantigo has already connected this Microsoft account.* | Tell your administrator: the account is connected to someone else's user. |
| *Vantigo knows no Microsoft account of yours that it manages, so there is nothing to connect.* | Ask HR to connect your person in Staff to your Microsoft account. |
| *The Outlook connection is turned off for this workspace.* | Ask an Owner to turn it on. |

If Microsoft shows its own error page instead of coming back — for example one naming
`AADSTS50011` or saying approval is required — Vantigo's registration in your
organisation is not finished: tell your administrator.

## Connect your Google account

The **Google** card is the same for a company on Google Workspace: you connect your own
Google account once, and Vantigo keeps the connection until you disconnect it, leave, or
your administrator turns it off. Nothing uses it yet: calendar sync to Google Calendar and
sending from your Gmail address come in later releases, each on this one connection.

The **Google** card offers a connection only when two things hold:

- an Owner has turned on **Connection for people** on the Google Workspace page's
  **Calendar and Gmail** tab ([Google Workspace](/en/user/google-workspace/#the-calendar-and-gmail-tab)),
  and the organisation's web client is set up; and
- HR has connected your person in Staff to a Google account that Vantigo manages — one
  it created, or whose adoption someone confirmed — and that account is not suspended.

Otherwise the **Google** card is not shown.

1. Open **Settings → Connections**. The **Google** card says **Not connected**.
2. Choose **Connect Google**. Your browser goes to Google, with your work account filled
   in. Sign in if Google asks, then allow what Vantigo asks for on Google's page. Google
   always shows this page, even when you have connected before; for your organisation's
   own app it is one click. If Google lists the items one by one, **allow every one**:
   Vantigo asks only for what the feature needs.
3. Back in Vantigo, the card finishes the connection and returns you to where you
   started.

You can also connect from the composer or the calendar wherever a **Connect Google**
button appears beside **Connect Outlook**; it brings you back there.

Sign in with **the account Vantigo knows you by**: the one HR connected to your person.
Signing in as anyone else — another account in your organisation, a personal Gmail
account, an account from another organisation — is refused, and nothing is connected.

### Connected to Google

The card says **Connected as** and your account's address, **Since** the day you
connected, and **Used by:** the features the connection serves: *Calendar* today, *Gmail* once your
organisation uses it to send. Choose **Disconnect**, then confirm, to stop: Vantigo
stops using your Google account until you connect again, and later, once nothing needs
it, tells Google to forget the connection. Disconnecting does not sign you out of
Google or of Vantigo. You can also end the connection yourself in your Google account's
security page, under the apps with access to your account; Vantigo then shows
**Reconnect needed** the next time it uses the connection.

### Reconnect needed for Google

| The card says | Why |
| --- | --- |
| *Google no longer accepts the connection: it was revoked, it expired, or your password changed. Connect again.* | You removed Vantigo's access in your Google account, the connection went unused for six months, or a password change ended it. |
| *Your organisation's Google policy blocks the connection. Ask your Google Workspace administrator.* | An administrator restricted Vantigo's web client in the Admin console, or your account is outside the organisation the app is for. |
| *Google did not grant everything Vantigo asked for. Connect again and allow every item on Google's page.* | You, or a change at Google, removed part of what the connection needs. |
| *Sign in to Google again to keep the connection.* | Google refused the connection for another reason. |

Only you see this. Your administrator sees how many people wait to reconnect on the
Google Workspace page. If you leave, or your Google account is suspended or deleted, the
connection is closed for you and forgotten.

### When connecting to Google fails

The card says **Google could not be connected** and why:

| The card says | Do |
| --- | --- |
| *You cancelled on Google's page.* | Connect again, and allow on Google's page. |
| *You signed in with an account outside your organisation's Google Workspace.* | Choose your work account on Google's page, not a personal Gmail account. |
| *Google did not grant everything Vantigo asked for. Connect again and allow every item on Google's page.* | Connect again and leave every item ticked. |
| *The connect took too long, or was started in another session. Connect again.* | Connect again, and finish within ten minutes in the same browser. |
| *Google's answer could not be used. Try again in a moment.* | Try again; if it keeps happening, tell your administrator. |
| *You signed in to Google with another account than the one Vantigo knows you by. Connect again and choose that account.* | Choose your work account on Google's page; ask HR if Vantigo knows you by the wrong one. |
| *Another person in Vantigo has already connected this Google account.* | Tell your administrator: the account is connected to someone else's user. |
| *Vantigo knows no Google account of yours that it manages, so there is nothing to connect.* | Ask HR to connect your person in Staff to your Google account. |
| *Connecting Google is turned off for this workspace.* | Ask an Owner to turn on **Connection for people**. |

If Google shows its own error page instead of coming back — for example
`redirect_uri_mismatch` or *Access blocked* — the organisation's web client is not set
up for Vantigo: tell your administrator.
