---
title: "Objektlagring"
description: "Filsystem- og S3-leverandørene, flytting mellom dem, modulomfang, nøkkelvalidering og strømmede nedlastinger."
sidebar:
  order: 41
sources:
  - apps/server/internal/storage
  - apps/server/cmd/vantigo/storage.go
  - apps/server/internal/invoices/bankimport.go
  - apps/server/internal/invoices/bank_feed_sync.go
  - apps/server/internal/invoices/reminderpdf.go
  - apps/server/internal/invoices/print_batches.go
  - apps/server/internal/staff/objects.go
  - apps/server/internal/customers/objects.go
  - apps/server/internal/manufacturing/quality_photos.go
  - apps/server/internal/products/objects.go
  - apps/server/internal/identity/brand_logos.go
---
Objektlagring er applikasjonsporten Kommunikasjon mellomlagrer og serverer vedlegg
gjennom. Moduler snakker aldri med en leverandør-SDK: de får et `ObjectStore` avgrenset
til sitt eget navnerom og sender relative nøkler.

## Leverandører

To drivere implementerer porten, og `STORAGE_PROVIDER` velger én av dem:

- **`fs`** lagrer objektene under en lokal katalog, et montert volum i en container.
- **`s3`** lagrer dem i en bøtte hos en hvilken som helst S3-kompatibel tjeneste:
  UpCloud Managed Object Storage, AWS S3, MinIO, SeaweedFS og lignende.

Azure Blob-leverandøren som .NET-implementasjonen tilbød, finnes ikke i denne porten.
Begge driverne legger ut objektene på samme måte (se
[Modulomfang og den fysiske nøkkelen](#modulomfang-og-den-fysiske-nøkkelen)), så
`vantigo storage copy` flytter en installasjon fra den ene til den andre uten å gi noe
nytt navn.

## Konfigurasjon

| Variabel | Formål | Standard |
| --- | --- | --- |
| `STORAGE_PROVIDER` | `fs`, `s3`, eller ikke satt | ikke satt |
| `STORAGE_FS_ROOT` | Absolutt rotkatalog; påkrevd når leverandøren er `fs` | ikke satt |
| `STORAGE_FS_ALLOW_INSECURE_ROOT` | Lemp på kontrollen av gruppe-/verdensskrivbar rot | `0` |
| `STORAGE_S3_ENDPOINT` | Tjenestens URL, `https://vert[:port]` uten sti; `http` bare med `APP_ENV=development`. Påkrevd for `s3` | ikke satt |
| `STORAGE_S3_REGION` | Leverandørens regionnavn, for eksempel `europe-1`. Påkrevd for `s3` | ikke satt |
| `STORAGE_S3_BUCKET` | Installasjonens bøtte: 3–63 små bokstaver, sifre og bindestreker. Opprettes aldri av appen. Påkrevd for `s3` | ikke satt |
| `STORAGE_S3_PREFIX` | Et nøkkelprefiks inne i bøtta: små bokstaver, sifre og bindestreker, i segmenter skilt med `/` | ingen |
| `STORAGE_S3_ACCESS_KEY_ID` | Tilgangsnøkkelen; eller `STORAGE_S3_ACCESS_KEY_ID_FILE`, stien til en fil som inneholder den. Påkrevd for `s3` | ikke satt |
| `STORAGE_S3_SECRET_ACCESS_KEY` | Den hemmelige nøkkelen; eller `STORAGE_S3_SECRET_ACCESS_KEY_FILE`. Påkrevd for `s3` | ikke satt |
| `STORAGE_S3_FORCE_PATH_STYLE` | `1` adresserer bøtta i stien (`https://vert/bøtte/nøkkel`), `0` som underdomene | `1` |

```text
STORAGE_PROVIDER=fs
STORAGE_FS_ROOT=/var/lib/vantigo/objects
```

```text
STORAGE_PROVIDER=s3
STORAGE_S3_ENDPOINT=https://abc123.upcloudobjects.com
STORAGE_S3_REGION=europe-1
STORAGE_S3_BUCKET=acme
STORAGE_S3_ACCESS_KEY_ID_FILE=/run/secrets/s3/access-key-id
STORAGE_S3_SECRET_ACCESS_KEY_FILE=/run/secrets/s3/secret-access-key
```

Hver drivers variabler gjelder bare med dens leverandør: å sette `STORAGE_FS_ROOT`
uten `STORAGE_PROVIDER=fs`, eller en `STORAGE_S3_*` uten `STORAGE_PROVIDER=s3`, er en
konfigurasjonsfeil, ikke en stilltiende no-op. En nøkkel og dens `_FILE`-variant kan
ikke begge være satt; et linjeskift til slutt i en fil fjernes, og verdien kan ikke
inneholde mellomrom. Den hemmelige nøkkelen sladdes overalt der konfigurasjonen
skrives ut.

**Lagring som ikke er satt, feiler lukket ved hver operasjon, ikke ved oppstart.** Med
`STORAGE_PROVIDER` ikke satt starter prosessen likevel og betjener alt annet; hver
lagringsoperasjon rapporterer da `storage: not configured`. Det er med vilje: en
installasjon som aldri laster opp et vedlegg, trenger ikke lagring provisjonert for å
starte.

`STORAGE_FS_ALLOW_INSECURE_ROOT` godtas **bare når `APP_ENV=development`**; utenfor
development er den en oppstartsfeil uansett hva den ville ha gjort — samme form som
`MAIL_DRIVER=log` og `OWNERS_ALLOW_INSECURE_NO_MFA`.

## Klarhetskontrollen

Når lagring er konfigurert, kjører `GET /health/ready` en kontroll som heter `storage`
ved siden av `postgres`: en `HeadBucket` mot bøtta for `s3`, en stat av roten for `fs`.
En bøtte som mangler, nøkler som avvises, et endepunkt som ikke svarer eller en rot som
er borte, gir 503 med `storage` i `failing`; prosessen fortsetter å kjøre, så den
kommer seg av seg selv når bøtta svarer igjen. Uten lagring er det ingen kontroll.

Et lager som ikke engang kan bygges, er en oppstartsfeil, som før: for eksempel en
`fs`-rot som ikke kan opprettes eller skrives til. `s3`-driveren kontakter ingenting
ved oppstart, så problemene dens vises alltid i klarhetskontrollen i stedet.

## S3-driveren

- **Opplastinger strømmes.** Et objekt på opptil 16 MiB sendes som én `PUT` og holdes
  i minnet mens det sendes; alt større sendes som en multipart-opplasting i deler på
  16 MiB og holder omtrent to deler (32 MiB) i minnet, uansett hvor stor filen er.
  Ingenting skrives til lokal disk. Regn det med i podens minnegrense: en pod med mange
  store opplastinger samtidig trenger 32 MiB for hver.
- **Innholdstypen** lagres med objektet. En `Put` over en eksisterende nøkkel
  erstatter den.
- **Ingen integritetstrailere.** Driveren (bygd på `minio-go`) sender aldri de
  etterfølgende CRC-sjekksummene som nyere AWS-SDK-er legger til som standard, og som
  UpCloud og flere andre S3-kompatible tjenester avviser.
- **En nøkkel som mangler**, er «finnes ikke» for modulene, som på `fs`; å slette en
  nøkkel som mangler, lykkes.
- **Bøtta er aldri offentlig og aldri forhåndssignert**: nedlastinger strømmes
  fortsatt gjennom applikasjonens autoriserte endepunkter, som beskrevet under
  [Nedlastinger](#nedlastinger).
- **Bøtta opprettes eller konfigureres aldri av appen.** Opprett den, nøklene og
  policyen på forhånd.

Nøklene trenger disse handlingene på bøtta og ingenting annet: `s3:ListBucket` på
`arn:aws:s3:::<bøtte>` (for klarhetskontrollen og `vantigo storage copy`), og
`s3:GetObject`, `s3:PutObject`, `s3:DeleteObject` og `s3:AbortMultipartUpload` på
`arn:aws:s3:::<bøtte>/*`.

### UpCloud Managed Object Storage

- **Endepunkt:** `https://<tjeneste-id>.upcloudobjects.com`, eller
  `https://<tjeneste-id>-private.upcloudobjects.com` fra et privat nettverk.
- **Region:** regionnavnet med små bokstaver, `europe-1` for Helsingfors, ikke
  sonekoden (`fi-hel2`).
- **Adressering:** behold `STORAGE_S3_FORCE_PATH_STYLE=1`, standarden.
- **Én bøtte per installasjon, med sin egen bruker.** Opprett en bruker for
  installasjonen, knytt til en policy som gir handlingene over på den ene bøtta, og gi
  appen den brukerens tilgangsnøkkel. En bruker uten policy kan ingenting, så nøklene
  til én installasjon når ikke en annens bøtte.
- **Kryptering i hvile** er alltid på (AES-256, nøkler forvaltet av UpCloud); det er
  ingenting å konfigurere.

## Flytte mellom fs og s3

`vantigo storage copy` kopierer hvert objekt mellom en `fs`-rot og bøtta miljøet
konfigurerer. Kjør den med `s3`-innstillingene (`STORAGE_PROVIDER=s3` og
`STORAGE_S3_*`-variablene) og den gamle roten montert:

```sh
vantigo storage copy --fs-root /var/lib/vantigo/objects
```

Miljøet for kopieringen er installasjonens, med lagringsinnstillingene byttet ut:
`STORAGE_PROVIDER=s3` og `STORAGE_S3_*`-variablene, og ingen `STORAGE_FS_ROOT`, som
avvises uten `STORAGE_PROVIDER=fs` — roten oppgis med `--fs-root` i stedet.

`--to fs` kopierer den andre veien, fra bøtta inn i roten. Kommandoen hopper over et
objekt målet allerede har med samme størrelse, kopierer aldri `fs`-driverens
uferdige skrivinger (`.<navn>.<tilfeldig>.tmp`-filer), skriver ut hvor mange objekter
den kopierte og hoppet over, og avslutter med 1 ved første feil. Den er trygg å kjøre
igjen: en ny kjøring kopierer objektene som er nye eller har endret størrelse. Et
objekt som er skrevet om på samme sted med samme størrelse, hoppes også over; `--all`
kopierer hvert objekt uansett hva som allerede ligger der.

Slik flytter du en installasjon fra `fs` til `s3`:

1. Opprett bøtta, brukeren og policyen.
2. Kjør `vantigo storage copy` mens installasjonen betjener brukere. Det flytter det
   meste.
3. Stopp skrivingen: stopp prosessene `api`, `server` og `worker`, eller skaler dem
   til null.
4. Kjør `vantigo storage copy --all` igjen. Med skrivingen stoppet kopierer denne
   runden alt, så ingenting som ble skrevet om i mellomtiden, går tapt. Uten `--all`
   går den raskere og kopierer det som er nytt eller har endret størrelse.
5. Bytt `STORAGE_PROVIDER` til `s3`, sett `STORAGE_S3_*`-variablene, fjern
   `STORAGE_FS_ROOT`, og start.
6. Sjekk at `/health/ready` består og at et eksisterende vedlegg kan lastes ned. Behold
   det gamle volumet til du har gjort det.

Et objekt som slettes mellom de to kopieringene, blir liggende i bøtta som foreldreløst:
ingen modul viser til det lenger, så det serveres aldri.

## Midlertidige filer

Objekter går aldri via lokal disk med noen av driverne, men én funksjon skriver en
midlertidig fil: **Regnskaps SAF-T-import** holder den opplastede filen (opptil
`ACCOUNTING_SAFT_IMPORT_MAX_BYTES`, 512 MiB som standard) i den midlertidige katalogen
mens den leser forhåndsvisningen fra den og deretter lagrer den. Den midlertidige
katalogen er `$TMPDIR`, eller `/tmp` når den ikke er satt, og den må være skrivbar for
applikasjonsbrukeren og stor nok for den største importen. Med et skrivebeskyttet
rotfilsystem monterer du et skrivbart volum der.

## Merkevarelogoer

Identitet lagrer nå **merkevarelogoer** (merkene, ikonet og ordmerket i
[Merkevare](/nb/admin/branding/)) i sitt eget område, `identity`, som
`brands/logo-{sha256}.{png|jpg}`: oppkalt etter hashen av den normaliserte filen, så å laste opp
den samme logoen to ganger gir ett objekt, og **aldri slettet**, siden et dokument kan bære en.
Uten objektlager fungerer merkevarer fullt ut, uten logoer; **Merkevare**-skjermen sier hvorfor
opplastingskontrollene er av. Engangsimporten av `APP_LOGO_URL` trenger også et lager, og gir en
melding når det ikke finnes noe. Publiserte logoer serveres til alle uten innlogging fra
`/api/v1/identity/brand-assets/{sha256}.{png|jpg}`, som bare svarer for en fil en publisert
merkevare navngir.

## Filsystemdriveren

Roten må finnes eller kunne opprettes ved initialisering, må ikke selv være en
symbolsk lenke, og — utenfor development — må ikke være gruppe- eller verdensskrivbar.
Kataloger driveren oppretter har modus `0700`: applikasjonsidentiteten alene. Gi den
et volum applikasjonsbrukeren eier eksklusivt; pek den ikke på en delt skrivbar
katalog.

Innesperringen håndheves av operativsystemet, ikke av et par av kontroller-så-åpne.
Driveren åpner roten én gang som en `os.Root` og utfører hver operasjon gjennom den,
så hvert navn løses opp med `openat`-stil innesperring i bruksøyeblikket: en symlenke
plantet inne i roten i etterkant — selv en tidsbestemt til å lande mellom en kontroll
og systemkallet — kan fortsatt ikke få en operasjon til å havne utenfor roten.
Nøkkelvalideringen kjører uansett foran dette og avviser former med traversering,
URL-koding, absolutte stier, omvendt skråstrek, kontrolltegn og duplisert prefiks, så
meningsløs inndata får en tydelig typet avvisning før noe systemkall. De to utfyller
hverandre og er ikke overflødige.

Skrivinger går til en midlertidig fil under roten og flyttes atomisk på plass ved
omdøping, så et krasj midt i en skriving etterlater aldri et delvis skrevet objekt som
lesbart. Nøkler er begrenset til 1024 UTF-8-byte. **Ingen API returnerer noen gang en
fysisk filsystemsti.**

## Modulomfang og den fysiske nøkkelen

Hver modul får et avgrenset lager, og omfanget prefikserer hver nøkkel før den når
driveren. Den fysiske nøkkelen er:

```text
{scope}/{relative-key}
```

Det er stien under `STORAGE_FS_ROOT` for `fs` og objektnøkkelen i bøtta for `s3`,
bak `STORAGE_S3_PREFIX` og en `/` når et prefiks er satt.

**Det finnes ikke noe leietakersegment.** .NET-implementasjonens fysiske nøkkel var
`tenants/{tenant-id}/{scope}/{relative-key}`; dette er en applikasjon for én leietaker,
og leietakersegmentet er borte sammen med resten av flerleietakerskapet. Ingenting
slår opp en leietaker, og ingen lagringsoperasjon feiler i mangel av en.

Omfangsnavn er kanonisk ASCII med små bokstaver `[a-z0-9-]`, høyst 64 tegn, uten
skråstreker eller punktum. Kommunikasjon bruker omfanget `communications`, så ett av
dens vedlegg havner på `communications/<relative-key>`. Utgifter bruker omfanget
`expenses`: en kvitterings relative nøkkel er `receipts/<entryId>/<uuid>`, så dens
fysiske nøkkel er `expenses/receipts/<entryId>/<uuid>`. Fakturaer bruker omfanget
`invoices`: PDF-en til et utstedt dokument har den relative nøkkelen
`documents/<id>/<number>-<sha256>.pdf`, så dens fysiske nøkkel er
`invoices/documents/<id>/<number>-<sha256>.pdf` — lagret én gang etter at utstedelsen
er bekreftet, og aldri slettet eller overskrevet, siden et utstedt dokument er
regnskapsmateriale ([Fakturaer](/en/reference/invoices/#the-pdf)). Å sende et dokument
som EHF legger til tre til ved siden av, som oppbevares på samme måte: EHF-en slik den
ble overlevert, `documents/<id>/<number>-<sha256>.xml` (`application/xml`), lagret én
gang etter hashen når dokumentet sendes; og, når aksesspunktet bekrefter leveringen,
kvitteringen `documents/<id>/<number>-<transmission>-receipt.json` og kopien det leverte
`documents/<id>/<number>-<transmission>-delivered.xml`
([E-faktura](/nb/admin/e-invoicing/#objektene-som-skrives)). En importert bankfil
oppbevares slik den ble lastet opp, under `bank-files/<sha256>.ocr` (en OCR-giro-fil) eller
`bank-files/<sha256>.xml` (en camt.054-fil) — med hashen av innholdet som nøkkel, lagret
én gang før importens transaksjon og aldri slettet eller overskrevet, siden den er
dokumentasjonen for innbetalingene som bokføres fra den (bokføringsloven § 10); en import
som feiler etter å ha lagret den, etterlater et objekt som neste import av de samme
bytene bruker igjen ([Innbetalinger fra banken](/nb/admin/payments/)). En bunke
bankintegrasjonen hentet, oppbevares på samme måte, slik banken sendte innbetalingene, under
`bank-files/<sha256>.json` — en JSON-liste med transaksjonene som ble beholdt, aldri
utbetalingene, kortkjøpene eller de interne overføringene. Bankintegrasjonen trenger
objektlageret: uten et, eller med et som feiler, feiler hver lesing av en konto og prøves
igjen, og ingenting registreres ([Bankintegrasjonen](/nb/admin/payments/#bankintegrasjonen)). PDF-en til et
purrebrev lagres under `reminders/<invoiceId>/<reminderId>-<sentOn>-<sha256>.pdf` — med
hashen av innholdet som nøkkel, lagt inn bare når ingenting ligger der fra før, før brevet
sendes på e-post eller når det skrives ut, og aldri slettet eller overskrevet: et brev som
lages på nytt med andre tall (et nytt forsøk samme dag etter en betaling), får sin egen
nøkkel, og brevets rad viser til objektet det gikk med
([jobben](/en/reference/invoices/#the-worker)). PDF-en til et papirbrev lagres på samme
måte når utskriftsbunken skrives ut, under postleggingsdagen; en ny utskrift skriver det ut
igjen under en ny nøkkel, og bunkens samlede PDF lages fra brevenes rader hver gang den
lastes ned, og lagres aldri; et brev der PDF-en ikke kunne lagres da det ble skrevet ut,
lagres neste gang bunken bekreftes postlagt eller lastes ned, og hver av dem stopper ved
det første brevet lageret avviser, så et brudd koster en forespørsel bare ett tidsavbrudd
([papir og postlegging](/en/reference/invoices/#paper-and-posting)). Ansatte bruker
omfanget `staff`: et dokuments relative nøkkel er `people/<personId>/<uuid>`, så dets
fysiske nøkkel er `staff/people/<personId>/<uuid>` ([Ansatte](/en/reference/staff/#documents)). Kunder bruker
omfanget `customers`: et vedleggs relative nøkkel er `attachments/<uuid>`, så dets fysiske
nøkkel er `customers/attachments/<uuid>` — en nøkkel som ikke navngir noen kunde, siden en
sammenslåing flytter vedleggets rad til kunden som overlever, og lageret ikke kan gi nytt
navn ([Kunder](/en/reference/customers/#attachments)). Produkter bruker omfanget
`products-images`: et produktbildes relative nøkkel er `<productId>/<uuid>` og miniatyrbildets
`<productId>/<uuid>-thumb`, så de fysiske nøklene er `products-images/<productId>/<uuid>` og
`products-images/<productId>/<uuid>-thumb` ([Produkter](/en/reference/products/#images)). Lager bruker
omfanget `inventory-lots`: en partifils relative nøkkel er `lots/<uuid>`, så den fysiske nøkkelen
er `inventory-lots/lots/<uuid>` — en nøkkel som ikke navngir noe parti og aldri lages av
filnavnet ([Lager](/en/reference/inventory/#a-lots-files)). Produksjon bruker omfanget `manufacturing-quality`: et kontrollbildes relative nøkkel er
`checks/<uuid>`, så den fysiske nøkkelen er `manufacturing-quality/checks/<uuid>` — en
nøkkel som ikke navngir noen arbeidsordre ([Produksjon](/en/reference/manufacturing/#photos)). Et avgrenset lager
avviser en relativ nøkkel som er lik omfanget sitt eller allerede begynner med
`{scope}/`: kallere sender bare relative nøkler og må aldri bygge prefikset selv.

**Utgifters kvitteringer, spesielt.** De er det eneste denne modulen lagrer. En
kvittering leses av den som får se utgiften den hører til — samme regel som styrer
utgiften selv, ikke en egen regel — og alltid gjennom applikasjonens eget
nedlastingsendepunkt (nedenfor), aldri en direkte lesing fra lagringen. Et objekt
fjernes sammen med utgiften sin: ved en vanlig sletting forsvinner objektet når
fjerningen av databaseraden er bekreftet, og ved en mislykket eller avvist skriving
fjernes objektet som ble mellomlagret for den igjen, så en feil etterlater aldri et
objekt ingenting peker på. Det ene vinduet ingenting lukker, er at prosessen dør
mellom skrivingen av objektet og fullføringen av den kompenserende fjerningen eller
bekreftelsen — det finnes ingen bakgrunnsopprydder i dag, så en kvittering som blir
foreldreløs på den måten overlever forespørselen som forårsaket den. Se
[Utgifter](/en/reference/expenses/#receipts) for opplastingsreglene (typer, størrelse,
antall, sniffing) og hastighetsbegrensningen.

**Ansattes dokumenter, spesielt.** En persons arkiv: arbeidsavtaler, attester,
sertifikater, korrespondanse, helsedokumenter og virksomhetens egne kategorier, hver en
PDF, JPEG, PNG eller HEIC på høyst 10 MiB. Et dokument leses av personen selv, av
`staff:manage` (unntatt helsedokumenter) og av `staff:personal-data`, alltid gjennom
Ansattes eget nedlastingsendepunkt. Objektet skrives før raden og fjernes igjen hvis
raden avvises. En sletting — for hånd, eller av sletteregelens kjøring — fjerner raden og
fører objektet inn i Ansattes logg over objekter som skal fjernes
(`staff.object_deletions`) i én transaksjon; deretter fjernes objektet, og en fjerning
lageret avviser blir stående i loggen til Ansattes slettearbeider fullfører den, så et
dokument som skal slettes aldri overlates til tilfeldighetene. Loggradene et lagringsbrudd etterlater, er det man
bør følge med på: de er objektene Ansatte fortsatt skylder en fjerning. Slås modulen av,
blir objektene og loggradene liggende urørt til den slås på igjen. Se
[Ansatte](/en/reference/staff/#documents) for opplastingsreglene og
hastighetsbegrensningen.

**Kunders vedlegg, spesielt.** En kundes dokumenter — en kontrakt, en taushetsavtale,
korrespondansen bak en tidslinjehendelse — hver en PDF, JPEG, PNG eller HEIC på høyst 10
MiB, høyst 200 per kunde. Et vedlegg leses med `customers:timeline-view` og skrives med
`customers:timeline-manage`, alltid gjennom Kunders eget nedlastingsendepunkt. Objektet
skrives før raden og fjernes igjen hvis raden avvises. En sletting — for hånd, eller av en
privatpersons anonymisering — fjerner raden og fører objektet inn i Kunders logg over
objekter som skal fjernes (`customers.object_deletions`) i én transaksjon; deretter fjernes
objektet, og en fjerning lageret avviser blir stående i loggen til `customers-anonymisation`-
arbeideren, som tømmer den etter hver runde (gjennomganger på hundre rader, i høyst ti
minutter), med nye forsøk til lageret bekrefter, så en fil
som skal slettes aldri overlates til tilfeldighetene. En sammenslåing flytter radene og lar
objektene ligge. Loggradene et lagringsbrudd etterlater, er det man bør følge med på: de er
objektene Kunder fortsatt skylder en fjerning, navngitt i arbeiderens logg; med
`CUSTOMERS_ANONYMISATION_ENABLED=0` venter de, som planlagte anonymiseringer gjør. Se
[Kunder](/en/reference/customers/#attachments) for opplastingsreglene og
hastighetsbegrensningen.

**Lagers partifiler, spesifikt.** Et partis sertifikater, analyser og bilder — hver en PDF,
JPEG eller PNG på høyst 20 MiB, høyst 20 per parti. En fil leses med begge Lagers
visningsrettigheter og skrives med `inventory:lots-manage`, alltid gjennom Lagers eget
nedlastingsendepunkt. Objektet skrives før raden og fjernes igjen hvis raden avvises. En
sletting fjerner raden og fører objektet i Lagers logg over objekter som skal fjernes
(`inventory.object_deletions`) i én transaksjon; objektet fjernes så, og en fjerning lageret
avviser, blir stående i loggen for arbeideren `inventory.object-deletions`, som kjører hvert
femte minutt i arbeidermodus og prøver hvert objekt på nytt — to sekunder etter første feil,
doblet, høyst en time imellom — til lageret bekrefter. Loggradene et lagringsbrudd
etterlater, er det man bør følge med på: de er objektene Lager fortsatt skylder en fjerning,
navngitt i arbeiderens logg. Uten konfigurert objektlagring svarer en opplasting eller en
nedlasting 503 `storage_not_configured`, og ingenting registreres.

## Nedlastinger

Nedlastinger strømmes alltid gjennom et autorisert applikasjonsendepunkt.

**Lagringskontrakten har ingen operasjon for forhåndssignerte URL-er**, og ingen
leverandør omdirigerer noen gang en kaller til en direkte lagrings-URL.
Kommunikasjons nedlastingsendepunkt autoriserer vedlegget, laster det gjennom det
avgrensede lageret og returnerer bytene med den lagrede innholdstypen og et renset
filnavn; feltet `downloadPath` i DTO-ene er det samme applikasjonsendepunktet på samme
opprinnelse, aldri en lagringsplassering. Se
[Kommunikasjon](/en/reference/communications/) for vedleggets livssyklus.

Utgifters `GET /attachments/{id}` følger samme form: det autoriserer kvitteringen
gjennom utgiften den hører til, laster den gjennom modulens eget `expenses`-avgrensede
lager, og strømmer bytene tilbake med innholdstypen de ble sniffet som ved opplasting
og filnavnet slik det ble oppgitt — aldri en lagringsplassering av noe slag. Se
[Utgifter](/en/reference/expenses/#receipts).

Ansattes `GET /documents/{id}` gjør det samme gjennom sitt `staff`-avgrensede lager,
med dokumentets egen synlighet; et dokument hvis objekt ikke kan leses, svarer 503
`storage_unavailable`, ikke 404. Se [Ansatte](/en/reference/staff/#reading-changing-and-removing).

Kunders `GET /customers/{id}/attachments/{attachmentId}` gjør det samme gjennom sitt
`customers`-avgrensede lager, under tidslinjens leserettighet, med de samme hodene og den
samme 503-en for et objekt som ikke kan leses. Se
[Kunder](/en/reference/customers/#attachments).

Lagers `GET /lots/{id}/attachments/{attachmentId}` gjør det samme gjennom sitt
`inventory-lots`-avgrensede lager, under begge Lagers visningsrettigheter, med de samme
hodene. Se [Lager](/en/reference/inventory/#a-lots-files).

Produksjons `GET /work-orders/{id}/photos/{photoId}` gjør det samme gjennom sitt
`manufacturing-quality`-avgrensede lager, under Bruke Produksjon, med de samme hodene; det
vises på plass, som et bilde. En opplasting som intet kontrollresultat navngir etter et
døgn, fjernes av arbeideren `manufacturing.object-deletions`. Se
[Produksjon](/en/reference/manufacturing/#photos).
