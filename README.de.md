<p align="center">
  <img src="frontend/public/logo.svg" alt="Community" width="88">
</p>

<h1 align="center">Community</h1>

<p align="center"><b>Ein kleiner Server für deine Ecke im Fediverse: Mastodon-Apps, Reaktionen wie bei Misskey und Matrix-Chat – alles schon drin.</b></p>

<p align="center">
  <a href="README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.zh-TW.md">繁體中文</a> · <a href="README.ja.md">日本語</a> · <a href="README.ru.md">Русский</a> · <a href="README.fr.md">Français</a> · <b>Deutsch</b>
</p>

<p align="center">
  <img alt="Go" src="https://img.shields.io/github/go-mod/go-version/kasuganosora/community">
  <img alt="ActivityPub" src="https://img.shields.io/badge/ActivityPub-federated-8a2be2">
  <img alt="Matrix" src="https://img.shields.io/badge/Matrix-homeserver-0dbd8b">
  <a href="https://c.hime.at"><img alt="Demo" src="https://img.shields.io/badge/demo-c.hime.at-1e90ff"></a>
</p>

<p align="center">
  <img src="docs/images/desktop-home.jpg" alt="Community on desktop" width="72%">
  <img src="docs/images/mobile-about.jpg" alt="Community on mobile" width="22%">
</p>

Community ist ein selbst gehosteter Social-Server, geschrieben in Go und Vue. Timelines, Folgen, Antworten, Umfragen, Medien und Direktnachrichten sind dabei, und du kannst dich mit Leuten auf Mastodon, Misskey und im restlichen Fediverse austauschen. Chaträume kommen von einem Matrix-Homeserver, der im selben Programm steckt – du musst also nichts zusätzlich betreiben.

Live-Demo: **https://c.hime.at** (ein Testserver, bitte nicht ernsthaft nutzen).

## Warum Community?

Es gibt schon großartige Fediverse-Server. Das macht Community anders:

- **Nutze die Mastodon-Apps, die du schon hast.** Community implementiert die Mastodon-Client-API, du kannst dich also mit Mastodon-Apps anmelden (geprüft mit Toot! für iOS). Dazu kommen Funktionen im Misskey-Stil: Emoji-Reaktionen (Unicode und eigene), Avatar-Dekorationen und eine vollständige Weboberfläche. Reaktionen föderieren in beide Richtungen mit Misskey.
- **Chat inklusive.** Ein Matrix-Homeserver ist in denselben Server eingebaut. Deine Nutzer können Gruppenchats über die Weboberfläche beitreten oder Element und andere Matrix-Clients verwenden (inklusive Ende-zu-Ende-verschlüsselter Räume) und mit Leuten auf anderen Matrix-Servern schreiben. Ein Synapse daneben ist nicht nötig.
- **Klein und einfach zu betreiben.** Ein einziges Go-Binary liefert API, Web-App, Föderation und Matrix. Läuft mit PostgreSQL, MySQL oder SQLite. Redis ist Pflicht, Meilisearch für die Volltextsuche optional. Auf unserer Demo braucht der Hauptprozess etwa 82 MiB Arbeitsspeicher.
- **Den ganzen Server umziehen, Follower behalten.** Du kommst von Misskey oder Mastodon? Community importiert die komplette Instanz und behält Schlüssel und Adressen – die Follows bleiben bestehen. Siehe [Wechseln, ohne von vorn anzufangen](#wechseln-ohne-von-vorn-anzufangen).
- **Einrichtung im Browser.** Beim ersten Start öffnet sich ein Einrichtungsassistent (7 Sprachen): Neuinstallation oder Migration, Domain und Datenbank wählen, fertig. Danach sperrt sich der Assistent selbst.
- **Spricht deine Sprache.** Weboberfläche und Fehlermeldungen gibt es auf English, 简体中文, 繁體中文, 日本語, Русский, Français und Deutsch.
- **Der übliche Admin-Werkzeugkasten.** Relays, Verwaltung eigener Emoji, Domain-Sperren, Inhaltsfilter, Speicher über S3 / Cloudflare R2 / MinIO, ein Medien-Proxy für entfernte Bilder und eine `tootctl`-CLI im Mastodon-Stil.

## Wechseln, ohne von vorn anzufangen

Das ist meist der Grund, warum niemand seine Fediverse-Software verlässt: Jedes Konto hat ein kryptografisches Schlüsselpaar und eine feste Adresse (seine „Actor“-URL), und jeder Beitrag hat ebenfalls eine feste URL. Andere Server prüfen Signaturen mit diesen Schlüsseln. Installierst du eine andere Software, ändern sich Schlüssel und URLs, Signaturen schlagen fehl und alle Follow-Beziehungen brechen. Also geben Admins auf und bitten alle, bei null anzufangen oder ihre Konten einzeln umzuziehen.

Community löst genau das. Es importiert **deine komplette Misskey- oder Mastodon-Instanz unter derselben Domain** und behält:

- die **ursprüngliche Actor-URL und das RSA-Schlüsselpaar** jedes Kontos (auch den Instanz-/System-Actor und dessen Schlüssel),
- die **ursprünglichen Beitrags-URLs** (Misskey `/notes/<id>`, Mastodon `/users/<name>/statuses/<id>`) und alte Medienlinks,

damit andere Server dir weiter vertrauen und **deine Follower Follower bleiben**. Niemand muss einen „Move“ durchführen.

**Was mitkommt**

- Konten, Profile, Profilfelder, Avatare und Header, Privatsphäre-Einstellungen
- Passwörter (kompatible bcrypt-Hashes – alle melden sich mit ihrem alten Passwort an)
- Beiträge, Antworten, Boosts, Inhaltswarnungen, nur-lokale Beiträge, Anhänge (echte Dateien, mit Prüfsumme) und unterstützte Umfragen
- Direktnachrichten und ihre Unterhaltungen, Empfänger bleiben privat
- Follows und offene Follow-Anfragen, Blockierungen, Stummschaltungen (mit Ablauf), Domain-Sperren, Listen, Lesezeichen, Likes
- Eigene Emoji und Emoji-Reaktionen
- Alles andere landet in einem privaten Archiv, und der Vorab-Prüfbericht zeigt genau, was archiviert wurde

**Was nicht mitkommt**

- Anmeldesitzungen und App-Token (OAuth): Nutzer melden sich neu an
- Zwei-Faktor-Authentifizierung und Sicherheitsschlüssel: Hat ein Konto 2FA aktiviert, stoppt die Vorab-Prüfung und sagt dir Bescheid, statt sie stillschweigend abzuschalten
- Alte Benachrichtigungen; der Verlauf wird nicht erneut an andere Server gesendet
- Manche Misskey-eigenen Funktionen (bestimmte Kanal-, Zitat- und Chat-Funktionen) werden eventuell nur archiviert
- Dein DNS: Die Domain stellst du selbst um, wenn du so weit bist

**Gefahrlos ausprobieren.** Die Exporter lesen deine alte Datenbank in einer Nur-Lese-Transaktion und verändern sie nie. Du kannst also auf einer separaten Community-Datenbank proben und jederzeit zum alten Server zurück. Denselben Import erneut auszuführen erzeugt keine Duplikate.

**So geht's:** Wähle im Einrichtungsassistenten „Von Misskey migrieren“ oder „Von Mastodon migrieren“, oder nutze die CLI `community-transfer`. Nutzer können außerdem ihre persönlichen Misskey-/Mastodon-Backups ins eigene Konto importieren. Getestet mit Misskey 2026.10.0 und Mastodon 4.7.3. Details, Checkliste und Grenzen: [docs/data-migration.md](docs/data-migration.md).

## Im Vergleich

Ein kurzer, ehrlicher Überblick. Jedes dieser Projekte hat seine Stärken – nimm, was zu dir passt.

| | Community | Mastodon | Misskey | GoToSocial | Akkoma |
| --- | --- | --- | --- | --- | --- |
| Sprache | Go | Ruby + Node.js | TypeScript (Node.js) | Go | Elixir |
| Laufende Prozesse | 1 App + Redis | web, streaming, Sidekiq + Redis | App + Redis | 1 App | 1 App |
| Datenbanken | PostgreSQL, MySQL, SQLite | PostgreSQL | PostgreSQL | PostgreSQL, SQLite | PostgreSQL |
| Mastodon-Apps | ✅ (siehe Hinweise) | ✅ nativ | ❌ | ✅ | ✅ |
| Emoji-Reaktionen | ✅ | ❌ | ✅ | ❌ | ✅ |
| Eingebaute Web-App | ✅ | ✅ | ✅ | Minimal (Apps nutzen) | ✅ |
| Eingebauter Matrix-Chat | ✅ | ❌ | ❌ | ❌ | ❌ |
| Einrichtungsassistent im Web | ✅ | ❌ (CLI) | ❌ | ❌ | ❌ (CLI) |
| Komplette Misskey-/Mastodon-Instanz unter derselben Domain importieren | ✅ | ❌ | ❌ | ❌ | ❌ |

**Wenn du GoToSocial wegen seiner Leichtigkeit magst:** Community spielt in derselben Gewichtsklasse und bringt zusätzlich eine vollständige Web-App, Reaktionen im Misskey-Stil, Matrix-Chat und Instanz-Migration mit. GoToSocial braucht kein Redis, Community schon.

**Gut zu wissen vor dem Wechsel:**

- Bearbeiten funktioniert als „löschen und neu posten“: Es entsteht ein neuer Beitrag, Likes und Antworten wandern nicht mit. Der Mastodon-Endpunkt zum Bearbeiten liefert `405`, Apps, die darauf setzen, müssen sich anpassen.
- Reaktionen laufen über eine API-Erweiterung von Community; normale Mastodon-Apps zeigen sie als Favoriten oder gar nicht.
- Es ist ein junges Projekt. Was genau mit welcher Software getestet wurde, steht in [docs/federation.md](docs/federation.md).

## Schnellstart

Du brauchst Docker mit Compose v2.24 oder neuer.

```bash
git clone https://github.com/kasuganosora/community.git
cd community
docker compose up -d --build
docker compose logs community | grep -A3 "install token"
```

Öffne dann `http://<dein-server>:8080/setup`, füge das Token ein und folge dem Assistenten. Für einen öffentlichen Server schalte einen HTTPS-Reverse-Proxy davor und verwende gleich deine endgültige Domain: Sie wird Teil der Identität jedes Kontos und sollte später nicht mehr geändert werden.

Weitere Optionen (manuelle `.env`, externe Datenbanken, S3): [docs/setup.md](docs/setup.md) und [docs/configuration.md](docs/configuration.md).

## Was du brauchst

| Stufe | Hardware | Aufbau | Was du bekommst |
| --- | --- | --- | --- |
| **Minimum** (eingeschränkt) | 1 vCPU, 1 GB RAM | SQLite + Redis, ohne Meilisearch, eine Handvoll Nutzer | Alles läuft, aber ohne Volltextsuche (Rückfall auf die Datenbank), langsamere Medienverarbeitung und langsameres Nachladen aus der Föderation |
| **Empfohlen** | 2 vCPU, 2–4 GB RAM | PostgreSQL + Redis, Meilisearch optional | Alle Funktionen |
| **Mittelgroß und größer** | Abhängig von Nutzerzahl und Föderationsverkehr | | Siehe [docs/tuning.md](docs/tuning.md) |

Was wir gemessen haben (in Docker mit harten CPU-/Speicherlimits und ohne Swap, 17. Oktober 2026; 10 Nutzer und 8 parallele Clients haben 10 Minuten lang Timelines gelesen, gepostet, Umfragen erstellt, Medien hochgeladen und geliked):

- **Minimum-Stufe** (gesamter Stack auf 1 CPU / 1 GB begrenzt): Community brauchte 78 MiB im Leerlauf und 196 MiB in der Spitze, Redis unter 10 MiB. Keine Abbrüche wegen Speichermangels. Etwa 97 Anfragen/s, Median 71 ms, 95. Perzentil 198 ms.
- **Empfohlene Stufe** (PostgreSQL + Redis + Meilisearch, 2 CPUs): Community erreichte in der Spitze 215–250 MiB, PostgreSQL ~300 MiB, Redis ~30 MiB. Mit 2 GB insgesamt wurde Meilisearch (auf 704 MiB begrenzt) wegen Speichermangels beendet, Community lief weiter und die Suche fiel auf die Datenbank zurück; mit 4 GB lief alles, Meilisearch kam auf 733 MiB. **Wenn du Meilisearch willst, plane 4 GB ein** (oder gib ihm allein mindestens 1 GB).

Auf dem Demo-Server (Oktober 2026) braucht Community etwa 82 MiB, Redis 141 MiB und Meilisearch 932 MiB – Meilisearch ist mit Abstand der größte Brocken, deshalb ist es optional. Rohdaten und wie man sie nachmisst: [docs/benchmarks.md](docs/benchmarks.md). Für Video-Vorschaubilder wird FFmpeg benötigt (im Docker-Image enthalten).

## Kompatibilität

- **Fediverse:** getestet mit Misskey und Mastodon (Folgen, Antworten, Direktnachrichten, Löschen, eigene Emoji, Reaktionen, Medien). ActivityPub-Relays werden ebenfalls unterstützt.
- **Matrix:** getestet mit Synapse und Element Web, inklusive verschlüsselter Räume und Anhänge.
- **Mastodon-Apps:** geprüft mit **Toot! (iOS)** auf der Demo (Anmeldung und Timelines, Oktober 2026). Community implementiert die Mastodon-Client-API, daher sollten die meisten Mastodon-Apps ebenfalls funktionieren (beachte den Hinweis zum Bearbeiten oben).
- **Matrix-Apps:** geprüft mit Element Web.

Getestete Versionen und bekannte Grenzen: [docs/federation.md](docs/federation.md).

## Aktualisieren

`git pull && docker compose up -d --build`. Datenbankmigrationen laufen beim Start. **Sichere vorher Datenbank und `uploads/`.** Siehe [docs/setup.md](docs/setup.md#upgrading-an-existing-deployment-ops).

## Dokumentation

- [Einrichtungsassistent und Konfigurationsdatei](docs/setup.md)
- [Installation und Konfiguration](docs/configuration.md)
- [Datenmigration](docs/data-migration.md)
- [Funktionen im Detail](docs/features.md)
- [Föderation und Kompatibilität](docs/federation.md)
- [Admin-Konsole](docs/admin-console.md) · [tootctl](docs/tootctl.md)
- [Tuning](docs/tuning.md) · [Benchmarks](docs/benchmarks.md)
- [Architektur und Code-Übersicht](docs/architecture.md) · [Entwicklung und Tests](docs/development.md)
- Vertiefung: [Feed](docs/feed-runtime.md), [Empfehlungen](docs/recommendations.md), [Link-Vorschauen](docs/link-previews.md), [Matrix-Webgruppen](docs/matrix-web-groups.md), [Direktnachrichten Matrix ↔ ActivityPub](docs/matrix-activitypub-direct.md)

## Mitmachen

Issues und Pull Requests sind willkommen unter [GitHub Issues](https://github.com/kasuganosora/community/issues). Bei Kompatibilitätsproblemen gib bitte Client- bzw. Serverversion, Schritte zum Nachstellen und bereinigte Logs an. Lokal bauen und testen: [docs/development.md](docs/development.md).

## Lizenz

Es wurde noch keine Lizenz gewählt. Solange keine `LICENSE`-Datei hinzugefügt ist, sind alle Rechte vorbehalten.

## Danksagung

Community steht auf den Schultern von [Mastodon](https://github.com/mastodon/mastodon), [Misskey](https://github.com/misskey-dev/misskey), [GoToSocial](https://github.com/superseriousbusiness/gotosocial), [Akkoma](https://akkoma.dev/AkkomaGang/akkoma), der [Matrix](https://matrix.org)-Spezifikation, [Synapse](https://github.com/element-hq/synapse) / [Element](https://element.io) und der ganzen ActivityPub-Community. Danke!
