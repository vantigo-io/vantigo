import { type CatalogResources, registerCatalog } from "@vantigo/frontend-shell";

export const metaCatalog = {
  en: {
    meta: "Meta",
    metaDescription:
      "Connect Vantigo to Facebook Messenger through your own Meta app, so a Facebook Page can be a communications channel.",
    loadFailed: "The Meta settings could not be loaded.",
    tryAgain: "Try again",
    save: "Save",
    cancel: "Cancel",
    copy: "Copy",
    copied: "Copied",
    copyValue: "Copy {{value}}",
    notSet: "not set",

    publicHostMissingTitle: "This instance has no public host",
    publicHostMissing:
      "Meta posts messages to a public address, so the settings cannot be saved until the instance has a public host. Set PUBLIC_BASE_URL for the server and restart it.",

    appTitle: "Meta app",
    appBody:
      "Create a Meta app of the Business type with the Messenger product, then enter its app ID and app secret from the app's settings.",
    appId: "App ID",
    appIdDescription: "Digits only.",
    appSecret: "App secret",
    appSecretSet: "The app secret is set. Leave the field empty to keep it.",
    appSecretNotSet: "The app secret is not set yet.",
    appSecretDescription: "Stored sealed and never shown again.",
    humanAgent: "Meta has granted this app the Human Agent permission",
    humanAgentDescription:
      "With the permission a reply can be sent up to seven days after the customer's last message; without it, 24 hours. Tick this only once Meta has approved the permission for the app.",
    humanAgentNotPermitted:
      "Meta refused a reply because the app does not hold the Human Agent permission. Clear the Human Agent checkbox above and save, or ask Meta for the permission.",
    saved: "Saved",
    savedBody: "The Meta settings are saved.",
    couldNotSave: "The settings could not be saved",

    webhookTitle: "Webhook",
    webhookBody:
      "In the Meta app's Messenger settings, add a callback URL with the address and verify token below, and subscribe to messages for the Pages you connect.",
    webhookUrl: "Callback URL",
    webhookUrlMissing: "The address is known once the instance has a public host.",
    verifyToken: "Verify token",
    regenerate: "Regenerate",
    regenerateTitle: "Regenerate the verify token?",
    regenerateConfirm: "Meta's handshake is refused until you paste the new token into the app's webhook settings.",
    regenerated: "New verify token",
    regeneratedBody: "Paste it into Meta's webhook settings.",
    couldNotRegenerate: "The verify token could not be regenerated",

    pagesTitle: "Pages",
    pagesBody:
      "Add each Facebook Page by its access token. A system user's token, which does not expire, is recommended; a personal token stops working when the person's password or permissions change.",
    noPages: "No Page is connected yet.",
    pageId: "Page ID {{id}}",
    "state.active": "Active",
    "state.token_rejected": "Token rejected",
    tokenRejectedHint:
      "Meta refused this Page's token. Add the Page again with a new token; channels on it are unavailable until then.",
    pageAdded: "Added {{date}}",
    pageChecked: "Checked {{date}}",
    accessToken: "Access token",
    accessTokenDescription:
      "A system user's token is recommended; a Page access token also works. Checked with Meta, the Page's own token is stored sealed and never shown again.",
    accessTokenRequired: "Enter the access token.",
    choosePage: "Choose the Page to connect",
    choosePageDescription: "The token manages several Pages that can be messaged.",
    choicePageId: "Page ID {{id}}",
    addPage: "Add Page",
    pageAddedNotice: "Page added",
    pageAddedBody: "{{name}} is connected.",
    removePage: "Remove",
    removePageLabel: "Remove {{name}}",
    removePageTitle: "Remove {{name}}?",
    removePageConfirm:
      "The Page's token, its queued events and their files are deleted, and channels on it become unavailable.",
    pageRemoved: "Page removed",
    couldNotRemove: "The Page could not be removed",

    statusTitle: "Status",
    lastWebhook: "Last webhook received",
    lastRejected: "Last post with a wrong signature",
    lastGraphError: "Last error from Meta",
    never: "Never",

    "graphError.190": "190: the Page's access token was rejected",
    "graphError.human_agent_not_permitted": "The Human Agent permission is not granted",

    "refusal.public_host_required":
      "The instance has no public host. Set PUBLIC_BASE_URL for the server and restart it before saving.",
    "refusal.not_configured": "Save the app ID and app secret first, then add the Page.",
    "refusal.page_token_insufficient":
      "The token is neither a Page's access token nor a user's or system user's token that manages a Page with permission to message. Use one that does.",
    "refusal.page_choice_required": "The token manages several Pages. Choose the one to connect.",
    "refusal.meta_unreachable": "Meta did not answer. Try again in a while.",
    "refusal.page_not_found": "That Page is no longer connected.",
    "refusal.too_many_requests": "That was asked too often. Wait a minute and try again.",
  },
  nb: {
    meta: "Meta",
    metaDescription:
      "Koble Vantigo til Facebook Messenger gjennom din egen Meta-app, slik at en Facebook-side kan være en kommunikasjonskanal.",
    loadFailed: "Meta-innstillingene kunne ikke lastes.",
    tryAgain: "Prøv igjen",
    save: "Lagre",
    cancel: "Avbryt",
    copy: "Kopier",
    copied: "Kopiert",
    copyValue: "Kopier {{value}}",
    notSet: "ikke satt",

    publicHostMissingTitle: "Denne instansen har ingen offentlig vert",
    publicHostMissing:
      "Meta sender meldinger til en offentlig adresse, så innstillingene kan ikke lagres før instansen har en offentlig vert. Sett PUBLIC_BASE_URL for serveren og start den på nytt.",

    appTitle: "Meta-app",
    appBody:
      "Opprett en Meta-app av typen Business med produktet Messenger, og skriv inn app-ID og apphemmelighet fra appens innstillinger.",
    appId: "App-ID",
    appIdDescription: "Bare sifre.",
    appSecret: "Apphemmelighet",
    appSecretSet: "Apphemmeligheten er satt. La feltet stå tomt for å beholde den.",
    appSecretNotSet: "Apphemmeligheten er ikke satt ennå.",
    appSecretDescription: "Lagres kryptert og vises aldri igjen.",
    humanAgent: "Meta har gitt denne appen tillatelsen Human Agent",
    humanAgentDescription:
      "Med tillatelsen kan et svar sendes opptil sju dager etter kundens siste melding, uten den 24 timer. Huk av først når Meta har godkjent tillatelsen for appen.",
    humanAgentNotPermitted:
      "Meta avviste et svar fordi appen ikke har tillatelsen Human Agent. Fjern haken for Human Agent ovenfor og lagre, eller be Meta om tillatelsen.",
    saved: "Lagret",
    savedBody: "Meta-innstillingene er lagret.",
    couldNotSave: "Innstillingene kunne ikke lagres",

    webhookTitle: "Webhook",
    webhookBody:
      "I Messenger-innstillingene til Meta-appen legger du til en callback-URL med adressen og verifiseringstokenet nedenfor, og abonnerer på meldinger for sidene du kobler til.",
    webhookUrl: "Callback-URL",
    webhookUrlMissing: "Adressen er kjent når instansen har en offentlig vert.",
    verifyToken: "Verifiseringstoken",
    regenerate: "Lag nytt",
    regenerateTitle: "Lage et nytt verifiseringstoken?",
    regenerateConfirm: "Metas håndtrykk avvises helt til du limer det nye tokenet inn i appens webhook-innstillinger.",
    regenerated: "Nytt verifiseringstoken",
    regeneratedBody: "Lim det inn i Metas webhook-innstillinger.",
    couldNotRegenerate: "Verifiseringstokenet kunne ikke lages på nytt",

    pagesTitle: "Sider",
    pagesBody:
      "Legg til hver Facebook-side med tilgangstokenet. Tokenet til en systembruker, som ikke utløper, anbefales; et personlig token slutter å virke når personens passord eller tillatelser endres.",
    noPages: "Ingen side er koblet til ennå.",
    pageId: "Side-ID {{id}}",
    "state.active": "Aktiv",
    "state.token_rejected": "Token avvist",
    tokenRejectedHint:
      "Meta avviste tokenet til denne siden. Legg til siden på nytt med et nytt token; kanaler på den er utilgjengelige til da.",
    pageAdded: "Lagt til {{date}}",
    pageChecked: "Sjekket {{date}}",
    accessToken: "Tilgangstoken",
    accessTokenDescription:
      "Et token for en systembruker anbefales; et tilgangstoken for selve siden fungerer også. Sjekkes hos Meta, og sidens eget token lagres kryptert og vises aldri igjen.",
    accessTokenRequired: "Skriv inn tilgangstokenet.",
    choosePage: "Velg siden som skal kobles til",
    choosePageDescription: "Tokenet forvalter flere sider som kan sendes meldinger til.",
    choicePageId: "Side-ID {{id}}",
    addPage: "Legg til side",
    pageAddedNotice: "Siden er lagt til",
    pageAddedBody: "{{name}} er koblet til.",
    removePage: "Fjern",
    removePageLabel: "Fjern {{name}}",
    removePageTitle: "Fjerne {{name}}?",
    removePageConfirm:
      "Sidens token, køede hendelser og filene deres slettes, og kanaler på siden blir utilgjengelige.",
    pageRemoved: "Siden er fjernet",
    couldNotRemove: "Siden kunne ikke fjernes",

    statusTitle: "Status",
    lastWebhook: "Siste webhook mottatt",
    lastRejected: "Siste innsending med feil signatur",
    lastGraphError: "Siste feil fra Meta",
    never: "Aldri",

    "graphError.190": "190: tilgangstokenet til siden ble avvist",
    "graphError.human_agent_not_permitted": "Tillatelsen Human Agent er ikke gitt",

    "refusal.public_host_required":
      "Instansen har ingen offentlig vert. Sett PUBLIC_BASE_URL for serveren og start den på nytt før du lagrer.",
    "refusal.not_configured": "Lagre app-ID og apphemmelighet først, og legg så til siden.",
    "refusal.page_token_insufficient":
      "Tokenet er verken tilgangstokenet til en side eller et token for en bruker eller systembruker som forvalter en side med tillatelse til å sende meldinger. Bruk et som er det.",
    "refusal.page_choice_required": "Tokenet forvalter flere sider. Velg den som skal kobles til.",
    "refusal.meta_unreachable": "Meta svarte ikke. Prøv igjen om litt.",
    "refusal.page_not_found": "Den siden er ikke lenger koblet til.",
    "refusal.too_many_requests": "Det ble spurt for ofte. Vent et minutt og prøv igjen.",
  },
} satisfies CatalogResources;

registerCatalog("meta", metaCatalog);
