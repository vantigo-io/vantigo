---
title: "Merkevare"
description: "Arbeidsområdets merkevare: navn, farge og logoer, utkast og publisering, engangsimporten av APP_TITLE og APP_LOGO_URL, og det som forblir Vantigos."
sidebar:
  order: 42
sources:
  - apps/server/internal/identity
  - openapi/identity.yaml
  - packages/frontend-shell/src/brand
  - apps/host/frontend/src/routes/workspace
---
En **merkevare** er hvordan arbeidsområdet ser ut for folkene i det: et navn og en tittel, én
farge, noen logoer og kontaktinformasjon for support. Vantigo har i dag én merkevare,
**standardmerkevaren**, som en Owner eller alle som har tillatelsen `identity:brands`
(«Administrer merkevare», kan delegeres som alle andre tillatelser under **Roller og tilgang**)
redigerer under **Arbeidsområde › Merkevare**. Skjermbildet er beskrevet for den som bruker det i
[Arbeidsområdeadministrasjon](/nb/user/workspace-administration/#merkevare).

En merkevare er aldri fri CSS. Du gir Vantigo **én hovedfarge**, og resten genereres, i lys og i
mørk. Derfor sjekkes fargen, og derfor finnes det ikke noe felt for noe annet.

## Hva en merkevare inneholder

| Del | Innhold |
| --- | --- |
| Navn | 1 til 100 tegn. Det administrasjonen viser. |
| Tittel | Opptil 60 tegn; navnet når den er tom. Produktnavnet i nettleserfanen, toppen og påloggingssiden. |
| Hovedfarge | Én `#rrggbb`. Vantigo bygger de ti nyansene fra den og beholder fargen din nøyaktig som nyanse 6. |
| Hjørneradius | `xs`, `sm`, `md`, `lg` eller `xl`. |
| Logoer | Et merke for lyse bakgrunner, et merke for mørke bakgrunner, et kvadratisk ikon og et valgfritt ordmerke. |
| Support | En e-postadresse, et telefonnummer og en lenke, alle valgfrie. |

Alt dette er deler av én **versjon**. Når du endrer noe av det, endrer du **utkastet**; den
publiserte versjonen, som er det folk ser, flytter seg ikke før du publiserer.

De genererte verdiene regnes ut på serveren hver gang et utkast lagres. API-et tar aldri imot en
fargeskala fra en klient: en forespørsel som inneholder `tokens`, `shades` eller noe annet enn
inndataene avvises med `400 invalid_request`.

## Fargen og hvorfor den sjekkes

Hovedfargen må være en som det går an å bygge en skala av: lysheten må ligge mellom 35 % og 85 %
(OKLCH). En farge nærmere hvit eller svart avvises med `422 brand_primary_range`.

Deretter sjekker Vantigo hvert par av farger merkevaren setter ved siden av hverandre, i **begge**
fargeskjemaene, mot WCAG AA (et forhold på minst 4,5 : 1 for tekst):

- teksten på en fylt knapp, og på den samme knappen med musen over;
- en lenke på siden, på et kort og på en tonet tabellrad.

Tekstfargen på en knapp velges, ikke antas: hvit på en mørk hovedfarge, svart på en lys. Et utkast
kan **lagres** selv om det feiler; det kan bare **publiseres** når alle par består. Redigeringen
viser hvert forhold og, når et par feiler, tilbyr den nærmeste hovedfargen som består med ett
klikk. En veldig lys hovedfarge kan feile lenkene, fordi de trenger en mørkere farge enn
hovedfargen selv. Hover-fargen på en knapp med svart tekst holdes mørk nok til å leses, så en klar
rød, oransje eller blå feiler den ikke.

Å publisere et utkast som feiler gjennom API-et avvises med `422 brand_contrast` og navngir parene
som feiler; bruk den foreslåtte naboen eller velg en mørkere farge.

Vantigos egen indigo (`#4f46e5`) beholder den håndjusterte skalaen produktet alltid har hatt, så
en installasjon som aldri redigerer merkevaren ser ut akkurat som før.

## Logoer

Logoer lastes opp på **Merkevare**-skjermen, én per plass, og følger de samme reglene som logoen
på fakturaer: en PNG eller en JPEG avgjort av filens egne byte (aldri SVG), høyst 512 KiB, mellom
16 og 2 000 piksler på sidene, og kodet på nytt ved opplasting slik at ingen metadata beholdes.
**Ikonet** må være kvadratisk innenfor 5 %. En merkevare uten merke for mørke bakgrunner viser det
lyse merket på en lys flate i mørk modus.

En opplastet logo havner i **utkastet** og vises bare for administratorer; den serveres til alle,
uten innlogging, når merkevaren er publisert. Filer lagres etter hash av innholdet og **slettes
aldri**, siden et dokument kan bære en. De ligger i `identity`-området i
[objektlageret](/nb/admin/object-storage/#merkevarelogoer). Uten objektlager fungerer merkevaren
fullt ut, uten logoer, og opplastingskontrollene sier hvorfor.

## Utkast, publisering og «forhåndsvis som meg»

1. Endre merkevaren og **Lagre utkast**. Bare administratorer kan se utkastet.
2. **Forhåndsvis som meg** setter hele programmet i utkastets utseende for deg, bare i denne
   nettleserfanen, med et banner for å avslutte. Det endrer aldri noe for noen andre.
3. **Publiser** gjør utkastet til merkevaren alle ser. Versjonsnummeret går opp. En person som har
   programmet åpent får den nye merkevaren neste gang siden lastes.
4. **Forkast utkast** fjerner den ventende versjonen.

Lagring og publisering bruker merkevarens revisjon (`If-Match`): to administratorer kan ikke
overskrive hverandre; den andre får beskjed om å laste på nytt.

## Hvordan siden får fargene uten blink

Merkevaren når nettleseren som et stilark, `GET /api/v1/identity/brand.css`, lenket i sidens
hode slik at den første tegningen allerede er riktig. Uten innlogging er det standardmerkevaren;
med innlogging er det den innloggede personens merkevare. For en innlogget person er svaret
`Cache-Control: private` med `ETag` og `Vary: Cookie`, så en delt hurtigbuffer aldri serverer én
persons merkevare til en annen. Står det en omvendt proxy foran, la `Vary: Cookie` passere.

Kan ikke stilarket lastes (frakoblet, en feilet forespørsel), tegner nettleseren den siste
merkevaren den speilet i lokal lagring, men bare for et speil av en versjon den forstår. Speilet
fjernes når personen logger ut.

Påloggingssiden, oppsettssiden, passordgjenoppretting, godtakelse av invitasjon og utloggingssiden
viser standardmerkevarens tittel, favorittikon og farger, fra `GET /api/v1/identity/brand/public`
(anonym, begrenset per adresse, bufret i ett minutt). Betalingssiden, RSVP-siden og stasjoner ber
ikke om noen merkevare i det hele tatt og beholder standardmerkevaren til fakturaer bærer sin egen.

## Engangsimporten av `APP_TITLE`, `APP_LOGO_URL` og `APP_SUPPORT_*`

Disse variablene brukte å profilere hele installasjonen. De **importeres nå én gang inn i
standardmerkevaren**, slik `OIDC_*` ble kortet for enkel pålogging:

- Ved første oppstart av en versjon med merkevarer oppretter API-et standardmerkevaren, publisert,
  av Vantigos egne verdier og de av variablene som er satt: tittelen (og navnet, fra den) og de tre
  supportkontaktene. Samme validering som i redigeringen gjelder; en kontakt som ikke er gyldig
  utelates og navngis i loggen.
- `APP_LOGO_URL` **hentes etter at programmet er oppe**, aldri mens det starter. Adressen må være
  `http` eller `https` (en sti som begynner med `/` leses fra installasjonens egen offentlige
  adresse); ingen omdirigering følges; den må svare innen 15 sekunder med høyst 2 MiB; og den kan
  ikke peke på en privat adresse, loopback-adresse, lenkelokal adresse eller skyleverandørens
  metadataadresse. Bildet må bestå logoreglene over og blir merket for lyse bakgrunner.
- Kan logoen ikke importeres (utilgjengelig, avvist adresse, ikke et bilde, over grensen, ikke noe
  objektlager) importeres alt det andre likevel, en advarsel logges og **Merkevare**-skjermen viser
  en melding om at du må laste opp logoen. Hentingen forsøkes ikke på nytt.
- Flere replikaer som starter samtidig oppretter **én** standardmerkevare, og bare én av dem
  henter logoen; en replika som stopper midt i hentingen erstattes etter to minutter.
- **Til importen er fullført** gjelder variablene akkurat som før, så en tilbakerulling til en
  eldre utgave mister ingenting. Etter den ignorerer programmet dem og logger en linje så lenge de
  fortsatt er satt. Du kan fjerne dem fra miljøet ditt.

## Det som forblir Vantigos

Dokumentasjonsstedet på `/docs` og API-referansen beholder Vantigos eget utseende. En merkevare
endrer nyansene i hovedfargen, lenker og tonede bakgrunner, hjørneradiusen og logoene; side-, kort-,
tekst- og kantfargene forblir Vantigos i begge skjemaene.

## Hva som kan gå galt

| Tegn | Årsak |
| --- | --- |
| `422 brand_primary_range` | Fargen er nesten hvit eller nesten svart. |
| `422 brand_contrast` | Et par er under 4,5 : 1; svaret navngir parene. |
| `422 brand_support_invalid` | E-posten har et visningsnavn, telefonen har andre tegn enn sifre, mellomrom og `+ - ( ) .` eller er over 30 lang, eller lenken er ikke `http(s)` eller en sti som begynner med én enkelt `/`. |
| `409 stale_revision` | Noen andre lagret merkevaren siden du åpnet den. |
| `400 logo_*` | Filen er ikke en akseptabel PNG eller JPEG: `logo_unsupported_type`, `logo_too_large`, `logo_too_wide`, `logo_too_small`, `logo_invalid`, `logo_not_square`. |
| `503 storage_unavailable` | Ikke noe objektlager, eller det kunne ikke skrives til. |
| En melding om logoen fra `APP_LOGO_URL` | Importen kunne ikke hente den; last opp logoen. |
