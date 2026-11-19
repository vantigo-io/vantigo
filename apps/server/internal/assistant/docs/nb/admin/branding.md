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
farge, noen logoer og kontaktinformasjon for support. Et arbeidsområde kan ha flere merkevarer, og regler
sier hvem som ser hvilken ([Flere merkevarer](#flere-merkevarer), [Hvem som ser hvilken
merkevare](#hvem-som-ser-hvilken-merkevare)). De redigeres av en Owner eller alle som har tillatelsen
`identity:brands`
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

## Flere merkevarer

Et arbeidsområde kan ha så mange merkevarer det vil. **Standardmerkevaren** er den en person ser
når ingen regel treffer dem; den finnes alltid. Under **Arbeidsområde › Merkevare** lister fanen
**Merkevarer** dem med status, standarden merket og hvor mange regler som peker på hver. Fra listen
kan du:

- **Opprette** en merkevare fra et navn og en hovedfarge. Den starter som et utkast som aldri har
  vært publisert, og treffer ingen regel før du publiserer den.
- **Duplisere** en merkevare: et nytt utkast laget av versjonen som redigeres, med logoer, med
  navnet «… (kopi)».
- **Gjøre til standard** en publisert merkevare. Den forrige standarden består som en egen merkevare.
- **Arkivere** en merkevare for å ta den ut av bruk. Den kan ikke lenger redigeres, tildeles eller
  velges, reglene dens fjernes, og valget til alle som hadde valgt den, nullstilles.
  Standardmerkevaren kan ikke arkiveres (`409 brand_in_use`); gjør en annen merkevare til standard
  først.
- **Gjenopprette** en arkivert merkevare: publisert igjen hvis den noen gang var det, ellers et
  utkast. Reglene dens gjenopprettes ikke.
- **Slette** en merkevare som aldri har vært publisert. En publisert merkevare arkiveres i stedet,
  fordi dokumenter kan nevne den. Reglene til en slettet merkevare forsvinner med den.
- **Eksportere** en merkevare som en JSON-fil og **importere** en: filen har navn, tittel, farge,
  radius og kontaktinformasjon for support, aldri logoene eller genererte farger. En import oppretter
  et utkast og regner alt ut på nytt, så en merkevare kan flyttes mellom en test- og en
  produksjonsinstallasjon.

Navnet på en merkevare er unikt uansett store og små bokstaver (`409 brand_name_taken`).

## Hvem som ser hvilken merkevare

En person ser én merkevare: sitt eget valg hvis de har ett og fortsatt kan ha det, ellers den første
tildelingsregelen som treffer dem, ellers standardmerkevaren. Den regnes ut ved hver forespørsel av
gjeldende data og lagres aldri, så en endring av gruppe eller ansettelse vises uten at noen må
tildele på nytt.

### Reglene

Fanen **Tildeling** har én ordnet liste med regler for hele arbeidsområdet. Den første regelen som
treffer en person, bestemmer merkevaren deres. En regel har en type og et mål:

| Type | Treffer en person når |
| --- | --- |
| E-postdomene | Domenet i e-postadressen deres er nøyaktig regelens domene: ingen jokertegn, og `mail.acme.no` treffer ikke `acme.no`. Skrives med små bokstaver og punycode. Et domene tilhører høyst én merkevare. |
| Tilgangsgruppe | De er effektive medlemmer av gruppen, regnet slik rollekoblingen regner (tvunget inn, eller til stede oppstrøms uten overstyring som tvinger dem ut), og gruppen er aktiv. |
| Juridisk enhet | De har et ansettelsesforhold i kraft i dag under en virksomhet i den juridiske enheten i Staff. Tilbys bare mens Staff er slått på. |

Dra en regel, eller bruk pilene, for å endre rekkefølgen; **Lagre regler** erstatter hele listen
på én gang, med listens revisjon som `If-Match`, så to administratorer kan ikke blande halve
redigeringer, og en avvist liste endrer ingenting. En ny regel settes der typen hører hjemme
(domener, så juridiske enheter, så grupper) og kan flyttes hvor som helst. Avvisninger:
`rule_duplicate` (samme mål to ganger), `domain_taken` (domenet tilhører en annen merkevare),
`domain_invalid`, `rule_target_not_found`, `rule_brand_invalid` (ingen slik merkevare, eller den er
arkivert), `staff_disabled` og `too_many_rules` (høyst 200), alle `422`, og `409 stale_revision`.

En regel hvis mål er borte (en slettet eller inaktiv gruppe, en arkivert juridisk enhet) beholdes,
merkes på skjermen og treffer ingen. Regler peker aldri på en arkivert merkevare. En integrasjon
eller en agent er ikke en person og får alltid standardmerkevaren.

### Personens eget valg

En person som treffes av flere merkevarer, kan velge mellom merkevarene til reglene som treffer
dem: avatarmenyen har da en seksjon **Merkevare** som lister dem, med **Automatisk** for å følge
reglene igjen. Standardmerkevaren er bare en kandidat for den ingen regel treffer. Valget lagres på
personens konto som fargeskjemaet, går foran reglene så lenge det er en av kandidatene, og
revideres ikke. Når reglene endres slik at det ikke lenger er det, ignoreres valget (menyen sier
fra) og det kommer tilbake hvis reglene gjør det. `PUT /api/v1/identity/account/brand` med en
merkevare som ikke er en kandidat, gir `409 brand_not_candidate`. Å arkivere en merkevare
nullstiller valget til alle som gjorde det.

### Hvem ser hva

Under **Hvem ser hva** på fanen **Tildeling** skriver du inn e-postadressen til en person for å se
hvilken merkevare de får og hvorfor: eget valg, regelen som traff eller standarden, og de andre
merkevarene de kunne valgt. Det krever `identity:brands`. Det viser ingen ansettelsesdetaljer: bare
regelen som traff, navngis. En som også har `staff:view`, mens Staff er slått på, får også
personens e-postdomene, tilgangsgrupper og juridiske enheter.

### Når Staff er av

Juridiske enheter kommer fra Staff gjennom en skrivebeskyttet kontrakt, `contracts.BrandTargets`.
Mens Staff ikke er slått på, tilbyr skjermen ingen regler for juridiske enheter, og de som allerede
står i listen, blir stående, merket og uvirksomme; de virker igjen når Staff slås på. En Staff-lesing
som feiler, feiler forespørselen i stedet for å vise standarden til en som skulle hatt en annen
merkevare.

### Hvordan åpne økter følger med

En publisering, en ny standard, en arkivering, en gjenoppretting, en sletting og en endring av
reglene kunngjøres til åpne økter på sanntidsforbindelsen (`identity.brand`); en persons eget valg
kunngjøres til deres andre økter på `identity.brand:{userId}`, som bare de kan abonnere på. En økt
henter merkevaren på nytt etter en tilfeldig ventetid på opptil fem sekunder, så en publisering
til mange åpne økter ikke blir like mange forespørsler samtidig. Det Staff sier om en persons
ansettelse, beholdes i 30 sekunder og deles mellom forespørsler som kommer samtidig; alt annet
leses på nytt.

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
3. **Publiser** gjør utkastet til merkevaren alle som er tildelt den, ser. Versjonsnummeret går
   opp. En person som har programmet åpent får den nye merkevaren i løpet av noen sekunder
   ([Hvordan åpne økter følger med](#hvordan-åpne-økter-følger-med)).
4. **Forkast utkast** fjerner den ventende versjonen.

Lagring og publisering bruker merkevarens revisjon (`If-Match`): to administratorer kan ikke
overskrive hverandre; den andre får beskjed om å laste på nytt.

## Hvordan siden får fargene uten blink

Merkevaren når nettleseren som et stilark, `GET /api/v1/identity/brand.css`, lenket i sidens
hode slik at den første tegningen allerede er riktig. Uten innlogging er det standardmerkevaren;
med innlogging er det den innloggede personens merkevare, den de er tildelt eller har valgt. For en
innlogget person er svaret `Cache-Control: private, no-store` med `ETag` og `Vary: Cookie`, så en delt hurtigbuffer aldri serverer én
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
  `http` eller `https`; ingen omdirigering følges; den må svare innen 15 sekunder med høyst 2 MiB;
  og den kan ikke peke på en privat adresse, loopback-adresse, lenkelokal adresse eller
  skyleverandørens metadataadresse. En sti som begynner med `/` (som `/logo.png`) navngir en av
  programmets egne filer og leses direkte fra dem, uten et kall til installasjonens adresse;
  basestien kan tas med eller utelates. Hele importen, objektlageret medregnet, må være ferdig
  innen 90 sekunder. Bildet må bestå logoreglene over og blir merket for lyse bakgrunner. En SVG
  godtas ikke.
- Kan logoen ikke importeres (utilgjengelig, avvist adresse, ikke et bilde, over grensen, ingen
  slik fil, ikke noe objektlager, for treg) ligger tittelen og supportkontaktene likevel i
  merkevaren, en advarsel logges og **Merkevare**-skjermen viser en melding om at du må laste opp
  logoen. Importen er da **ikke fullført**: `APP_TITLE` og `APP_LOGO_URL` gjelder som før til du
  laster opp en logo og publiserer merkevaren, og ved neste oppstart hentes logoen på nytt.
- Flere replikaer som starter samtidig oppretter **én** standardmerkevare, og bare én av dem
  henter logoen; en replika som stopper midt i hentingen erstattes etter to minutter.
- **Til importen er fullført** gjelder variablene akkurat som før, så en tilbakerulling til en
  eldre utgave mister ingenting. Den fullføres når logoen er importert, eller når en administrator
  publiserer standardmerkevaren med en egen logo. Etter den ignorerer programmet variablene fra
  neste oppstart og logger en linje så lenge de fortsatt er satt. Du kan fjerne dem fra miljøet
  ditt.

## Det som forblir Vantigos

Dokumentasjonsstedet på `/docs` og API-referansen beholder Vantigos eget utseende. En merkevare
endrer nyansene i hovedfargen, lenker og tonede bakgrunner, hjørneradiusen og logoene; side-, kort-,
tekst- og kantfargene forblir Vantigos i begge skjemaene.

## Hva som kan gå galt

| Tegn | Årsak |
| --- | --- |
| `422 brand_primary_range` | Fargen er nesten hvit eller nesten svart. |
| `422 brand_contrast` | Et par er under 4,5 : 1; svaret navngir parene. |
| `422 brand_support_invalid` | E-posten har et visningsnavn eller inneholder `?`, `&` eller `%`, telefonen har andre tegn enn sifre, mellomrom og `+ - ( ) .` eller er over 30 lang, eller lenken er ikke `http(s)` eller en sti som begynner med én enkelt `/`, eller inneholder et kontrolltegn som tabulator eller linjeskift. Samme regel gjelder `APP_SUPPORT_URL` og `APP_LOGO_URL` ved oppstart. |
| `409 stale_revision` | Noen andre lagret merkevaren, eller regellisten, siden du åpnet den. |
| `409 brand_name_taken` | En annen merkevare har navnet. |
| `409 brand_archived` | Du prøvde å redigere, publisere eller forkaste utkastet til en arkivert merkevare; gjenopprett den først. |
| `409 brand_in_use` | Du prøvde å arkivere standardmerkevaren, eller å slette en publisert merkevare eller standarden. |
| `409 brand_not_candidate` | En person valgte en merkevare reglene deres ikke gir dem. |
| `422 rule_duplicate`, `domain_taken`, `domain_invalid`, `rule_target_not_found`, `rule_brand_invalid`, `staff_disabled`, `too_many_rules` | En regelliste serveren avviste; `fields` navngir elementet. |
| `400 logo_*` | Filen er ikke en akseptabel PNG eller JPEG: `logo_unsupported_type`, `logo_too_large`, `logo_too_wide`, `logo_too_small`, `logo_invalid`, `logo_not_square`. |
| `503 storage_unavailable` | Ikke noe objektlager, eller det kunne ikke skrives til. |
| En melding om logoen fra `APP_LOGO_URL` | Importen kunne ikke hente den; last opp logoen og publiser. Til da gjelder variablene fortsatt. |
