---
title: Getting started
description: Accepting an invitation, signing in, your profile and security settings, and signing out.
sidebar:
  order: 1
sources:
  - apps/host/frontend
  - apps/server/internal/identity
---

This page covers getting into Vantigo and looking after your own account. For the
apps, the sidebar and search once you are in, see
[Finding your way around](/en/user/).

## Accept an invitation and create your account

Someone who administers the workspace invites you by email. The message carries a
link to the **Join Vantigo** page, which shows **Invitation for** your address and
asks for two things:

- **Display name** — how colleagues see you. If you leave it empty, the name the
  inviter entered is used, or your email address when they entered none.
- **Password** — at least 12 characters with an upper-case letter, a lower-case
  letter and a digit.

Choose **Create account**. You are signed in straight away and land on the
dashboard; if the workspace requires two-factor authentication for your role, you
land on the security settings instead (see
[the notice when the workspace requires it](#the-notice-when-the-workspace-requires-it)).

An invitation is for one address and works until it expires — seven days after it
was sent unless the installation is configured otherwise. **This invitation is
invalid or expired** means the link has expired, was revoked, or has already been
used; ask the person who invited you to send it again. An address that already has
an account cannot be invited.

### When your account was set up from the staff register

When your workplace registers you in Staff, your account may already exist before you
are invited: HR has set it up with your name, and the invitation only lets you choose
how to sign in. The link opens the same **Join Vantigo** page, which then says "Your
employer has set up this account for you, with your name and roles. Choose a password
to start using it." It asks no display name: your name is the one HR registered, and
you change it later under [your profile](#edit-your-profile). Choose a **Password** and
**Activate account**, and you are signed in to the account that was made for you, with
the roles it was given.

Such an invitation works only while the account is still waiting for you. If the
account has been disabled, completed or changed in the meantime, the link opens on
**This invitation is invalid or expired**, or, when that happens while the page is
open, **Create account** answers **This invitation is invalid or no longer
available**; ask HR or the workspace's Owner.

An administrator can also create your account with a password they choose and give
it to you directly. In that case, change the password under
[Security](#change-your-password) the first time you sign in.

## Sign in

Open Vantigo and the **Welcome back** page asks for your **Email** and **Password**.
Choose **Sign in**.

- **Sign in with a passkey** signs you in with a passkey you have added under
  Security, without the password. Enter your email first; the button is disabled
  until you do. Your browser or device then asks you to verify.
- **Continue with _provider_** appears when the installation is connected to an
  organisation identity provider (single sign-on). It takes you to the provider,
  which sends you back signed in. If the provider cannot complete the sign-in, the
  page says so and you can try again or use another method.
- **Forgot your password?** leads to the reset flow below.

When your account has an authenticator app enabled, a second page, **Verify your
sign-in**, asks for a **Security code**: the six-digit code from the app, or one of
your recovery codes. Choose **Verify**. **Use another sign-in method** takes you back
to the first page. A passkey sign-in counts as the second step, so it never asks for
a code.

The fifth wrong password in a row locks the account for 15 minutes. Wait, then try
again; a wrong authenticator code counts the same way.

## Reset a forgotten password

1. On the sign-in page, choose **Forgot your password?**.
2. On **Reset your password**, enter your **Email** and choose **Send reset link**.
   The page answers **Check your inbox for next steps** whether or not an account
   matches, so it never reveals which addresses exist.
3. Open the link in the email. On **Choose a new password**, enter a **New
   password** that meets the policy above and choose **Reset password**.
4. **Your password has been reset. You can sign in now.** Go back to sign in.

**This reset link is invalid or expired** means the link has already been used or
was issued too long ago; request a new one. An administrator can also send you a
reset email from the workspace's user list.

If your account signs in through an organisation identity provider, your password
lives there, not in Vantigo.

## When your session expires

A session ends on its own after a period without activity, and in any case some
hours after you signed in; administrators' sessions are shorter than everyone else's.
The next thing you do then shows **Your session has expired** with a **Sign in
again** button. Nothing you had already saved is lost.

You see the same page when an administrator disables your account, changes your
details or role, or signs you out everywhere, since each of those ends your sessions
at once. The exact limits are set by whoever runs the installation; see
[Session lifetime and revocation](/en/admin/authentication/#session-lifetime-and-revocation).

## Edit your profile

Open the avatar menu at the top right and choose **Settings**. The **Profile** page
("Your name, photo, language and time zone.") holds:

- **Profile photo** — **Choose a photo** (JPEG or PNG, up to 5 MB) and **Upload
  photo**. **Remove** takes it away again. The photo shows in the avatar menu and
  wherever colleagues see you, the Staff app's pages and Microsoft Entra ID included
  when you are in Staff. When HR has set a photo of you in Staff, that photo is your
  picture: the photo controls are greyed out with *Your photo is kept by Staff. Ask HR
  to change it.* Your own photo is kept and comes back when HR removes theirs
  ([Your photo](/en/user/staff/#your-photo) in Staff).
- **Name** — your display name.
- **Email** — shown but not editable here: "Your email is managed by your sign-in
  provider." An Owner changes it from the workspace's user list.
- **Preferred language** — **Automatic**, **Norwegian** or **English**.
- **Time zone** — **Same as the workspace**, or a zone of your own (below).

Choose **Save profile**. The language setting changes the whole interface at once,
and dates, numbers and currency follow it. **Automatic** follows your browser's
language. The choice is stored with your account, so it applies on every device you
sign in from. The public [pay page](/en/user/invoices/#what-the-customer-sees-on-the-pay-page)
your customers open from a pay link is not signed in and does not use it: it speaks the
language the invoice names, and the customer can switch. It asks Vantigo for nothing of
an account — no session, no maintenance notice — and sends no referrer to the sites it
links to. Calendar's [answer page](/en/user/calendar/#if-you-received-an-invitation),
which someone you invite opens from their invitation, is the same: not signed in, it
asks for no session, and it speaks the language the invitation was written in.

### Choose your time zone

Your time zone decides two things: the clock times Vantigo shows you — when a timeline
entry happened, a time stamp in a list — and what a time you
type means, such as the time of a customer meeting you record. It starts as **Same as
the workspace (Europe/Oslo)**, naming the workspace's business time zone, and keeps
following that zone if an administrator later changes it. To work in another zone —
you are travelling, or you work from another country — open the **Time zone** field on
the **Profile** page, search for your city's zone (for example `America/New_York`),
and choose **Save profile**. Every time on every page moves to it at once, on every
device you sign in from. Choose **Same as the workspace** again to go back.

It never moves a date. Today, a due date, an overdue follow-up, the date of an invoice
or a time entry are the workspace's bookkeeping days, the same for everyone whatever
their own zone, so an invoice issued on the 1st in Oslo is the 1st for you too, even
where your clock still says the 30th. If Vantigo does not accept the zone you chose,
the reason shows under the field and nothing is saved.

## Change your password

Under **Settings → Security**, the **Password** card asks for your **Current
password**, a **New password** and **Confirm new password**; choose **Change
password**. The new password must have at least 12 characters, with an upper-case
letter, a lower-case letter and a digit, and the two entries must match.

If the card says **Local password unavailable**, your account signs in with an
organisation identity provider; manage your password and security there. The
authenticator controls on the page are unavailable for the same reason.

## Set up an authenticator app

Two-factor authentication adds a code from an authenticator app (such as Google
Authenticator, Microsoft Authenticator or 1Password) to every password sign-in.

1. Under **Settings → Security**, in the **Authenticator app** card, choose **Set up
   authenticator app**, enter your password under **Current password to begin
   setup** and choose **Begin setup**.
2. A **Setup URI** appears. Add the account to your authenticator app with it: open
   it on the phone, or copy it into the app's add-account option.
3. Enter your **Current password** again together with the six-digit **Authenticator
   code** the app now shows, and choose **Enable**.
4. **Save your recovery codes now.** The codes are shown only this once. Each one
   signs you in a single time if you lose the phone, so keep them somewhere safe
   and separate from the phone.

The card then shows **Enabled**, with two actions:

- **Regenerate recovery codes** asks for your current password and an authenticator
  code, then shows a fresh set under **Save your recovery codes now**. The old
  codes stop working.
- **Disable** turns the second step off. It asks for your current password: "This
  is a sensitive change."

### The notice when the workspace requires it

The installation can require two-factor authentication for administrator accounts.
If you hold such an account and have no authenticator yet, Vantigo holds you at the
security settings with a yellow notice, **Set up two-factor authentication to
continue**, listing the steps above. Until you finish them, every other page brings
you back here, and the sidebar, the app switcher and search are hidden; the avatar
menu still works, so you can sign out. Access is restored the moment you choose
**Enable** — you do not need to sign in again.

## Add or remove a passkey

A passkey lets a device or security key sign you in instead of the password, and it
also counts as your second step. The **Passkeys** card under **Settings →
Security** lists the ones you have ("No passkeys registered." until you add one).

To add one, enter a **Passkey name** that tells you which device it is (for
example "MacBook"), your **Current password**, and choose **Add a passkey**. Your
browser or device then asks you to verify — with the fingerprint, face, PIN or
security key it uses. **Passkeys are not supported on this device or browser** means
this browser cannot create one; try another.

To remove one, choose **Remove** next to it. The **Remove passkey** dialog warns
that it cannot be undone and asks for your current password. A removed passkey can
no longer sign you in; add it again if you need it back.

## Sign out

Open the avatar menu at the top right and choose **Sign out**. You are returned to
the sign-in page. Signing out ends the session in this browser only; other browsers
or devices where you are signed in stay signed in until their own sessions expire.

## The dashboard and the app switcher

After signing in you land on the dashboard — **Home** in the app switcher at the top
of the page. It greets you by name; when Calendar is on and you may use it, it opens
with **Today and this week**, your appointments and dated items for today and the six
days after ([the dashboard](/en/user/calendar/#the-dashboard)). Below, it shows a card for each app you have access to,
with its key numbers for the chosen date range (**7d**, **30d**, **90d**, **12m** or
**Custom**), a **Needs attention** list of things waiting for you — each opens where
it is dealt with, such as the person's page in Staff, or the **Microsoft Entra ID**
page for an account in the tenant whose adoption is waiting to be confirmed — and
**Quick actions** for the things you do most. Staff has no numbers yet, so it is listed only
under **Modules** at the bottom of the page. A new workspace also shows **Finish your
setup**, a short list of first steps (the first customer, a mailbox, a product, a
project…) that disappears once they are all done.

The app switcher lists every app you may open; an app the installation has not
enabled is shown as **Not enabled**. A system administrator lands on **System admin**
instead of the dashboard. How the apps, the sidebar and search fit together is
described in [Finding your way around](/en/user/).
