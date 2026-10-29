---
title: Connections
description: Connecting your own Outlook to Vantigo under Settings → Connections — who sees it, what the sign-in to Microsoft looks like, Connected as, disconnecting, Reconnect needed and its reasons, and what each message when a connect fails means.
sidebar:
  order: 2
sources:
  - apps/host/frontend/src/routes/settings/connections.tsx
  - apps/entra/frontend/src/components/outlook-connection.tsx
---

**Settings → Connections** lists the accounts elsewhere that you connect to Vantigo.
Today that is your **Outlook**: you connect it once, signing in to Microsoft, and Vantigo
keeps the connection until you disconnect it, leave, or your administrator turns it
off. In this release nothing uses the connection yet — the calendar's [Outlook
sync](/en/user/calendar/#sync-with-outlook) still works as before — but connecting now
means nothing more is asked of you when it does.

## Who sees it

**Connections** is in the avatar menu's **Settings**, beside **Profile** and
**Security**, while your organisation runs the Microsoft Entra ID module; without it the
page does not exist. Everyone signed in sees their own connection and nobody else's; no
permission is needed.

The **Outlook** card offers a connection only when two things hold:

- an Owner has turned on the **Outlook connection** on the Microsoft Entra ID page
  ([Microsoft Entra ID](/en/user/entra/#outlook-connection)); and
- HR has connected your person in Staff to a Microsoft account that Vantigo manages —
  one it created, or whose adoption someone confirmed. Vantigo never guesses your
  account from your address.

Otherwise the card says *Nothing to connect: the Outlook connection is turned off, or
Vantigo knows no Microsoft account of yours.*

## Connect your Outlook

1. Open **Settings → Connections**. The **Outlook** card says **Not connected**.
2. Choose **Connect Outlook**. Your browser goes to Microsoft's sign-in, with your work
   account filled in. If you are already signed in to Microsoft 365, it usually comes
   straight back without showing a page; otherwise sign in as you normally do, with
   multi-factor authentication if your organisation asks for it.
3. Back in Vantigo, the card says *Finishing connecting Outlook…*, then **Outlook
   connected**, and returns you to where you started.

You can also connect from the **Outlook** card under **Calendar → My calendar**, which
shows the same button and brings you back there ([Sync with
Outlook](/en/user/calendar/#sync-with-outlook)).

Sign in with **the account Vantigo knows you by**: the one HR connected to your person.
Signing in as anyone else — a second account, a personal Microsoft account, an account
from another organisation — is refused, and nothing is connected.

## Connected

The card says **Connected as** and your account, **Since** the day you connected, and
**Used by:** the features the connection serves, such as *Calendar*.

## Disconnect

Choose **Disconnect**, then **Disconnect** in *Disconnect Outlook?*. Vantigo stops using
your Outlook until you connect it again, and the card says **Not connected**.
Disconnecting does not sign you out of Microsoft or of Vantigo, and in this release it
does not change the calendar's Outlook sync, which has its own switch.

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

Only you see this; it is not your administrator's failure, and nothing else in Vantigo
stops. Your administrator sees how many people wait to reconnect on the Microsoft Entra
ID page.

If you leave, or your Microsoft account is disabled or deleted, the connection is closed
for you.

## When connecting fails

When a connect cannot complete, the card says **Outlook could not be connected** and
why:

| The card says | Do |
| --- | --- |
| *The sign-in to Microsoft was cancelled or refused. Nothing was connected.* | Connect again, and accept on Microsoft's page. |
| *You signed in with an account from another organisation. Connect again and sign in with your work account.* | Sign out of the other account at Microsoft, or choose your work account. |
| *The connect took too long, or was started in another session. Connect again.* | Connect again, and finish within ten minutes in the same browser. |
| *Microsoft's answer could not be used. Try again in a moment.* | Try again; if it keeps happening, tell your administrator. |
| *Your organisation has not consented to the permissions the connection needs. Ask your administrator to grant admin consent.* | Your administrator grants admin consent ([the administration guide](/en/admin/entra/#outlook-connection)). |
| *You signed in to Microsoft with another account than the one Vantigo knows you by. Connect again and sign in with that account.* | Sign in with your work account; ask HR if Vantigo knows you by the wrong one. |
| *Another person in Vantigo has already connected this Microsoft account.* | Tell your administrator: the account is connected to someone else's user. |
| *Vantigo knows no Microsoft account of yours that it manages, so there is nothing to connect.* | Ask HR to connect your person in Staff to your Microsoft account. |
| *The Outlook connection is turned off for this workspace.* | Ask an Owner to turn it on. |

If Microsoft shows its own error page instead of coming back — for example one naming
`AADSTS50011` or saying approval is required — Vantigo's registration in your
organisation is not finished: tell your administrator.
