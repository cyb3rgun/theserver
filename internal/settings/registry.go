package settings

func bound(n int64) *int64 { return &n }

// registry is every setting of theserver, in the order of the configuration
// file. Settings of one section stand together.
var registry = []Setting{
	{
		Key: "server.listen_addr", Section: "server", Kind: Addr, Default: ":8443",
		Restart: true, Flag: "listen",
		Text: map[string]Text{
			"en": {
				Label:       "Listen address",
				Description: "Address and port the HTTPS server listens on.",
				Why:         "Devices and browsers reach theserver here. :8443 listens on every network interface of this computer, 127.0.0.1:8443 only on the computer itself. Change it when another program already uses the port, or when the server must only be reachable locally.",
			},
			"de": {
				Label:       "Adresse und Port",
				Description: "Adresse und Port, auf denen der HTTPS-Server lauscht.",
				Why:         "Geräte und Browser erreichen theserver hier. :8443 lauscht auf allen Netzwerkschnittstellen dieses Rechners, 127.0.0.1:8443 nur auf dem Rechner selbst. Ändern Sie den Wert, wenn ein anderes Programm den Port schon belegt oder der Server nur lokal erreichbar sein soll.",
			},
		},
	},
	{
		Key: "server.data_dir", Section: "server", Kind: Path, Default: "./data",
		Restart: true, Flag: "data-dir",
		Text: map[string]Text{
			"en": {
				Label:       "Data directory",
				Description: "Directory for the database, the certificate and the admin key.",
				Why:         "Everything theserver keeps between two starts lives here: the journal, the device registry, the TLS certificate and the key of the admin logins. Back this directory up. To move it, stop the server and move all of it.",
			},
			"de": {
				Label:       "Datenverzeichnis",
				Description: "Verzeichnis für die Datenbank, das Zertifikat und den Admin-Schlüssel.",
				Why:         "Alles, was theserver zwischen zwei Starts behält, liegt hier: das Journal, das Geräteverzeichnis, das TLS-Zertifikat und der Schlüssel der Admin-Anmeldungen. Sichern Sie dieses Verzeichnis. Um es zu verschieben, stoppen Sie den Server und verschieben Sie es vollständig.",
			},
		},
	},
	{
		Key: "tls.cert_file", Section: "tls", Kind: Path, Default: "", Empty: true,
		Restart: true,
		Text: map[string]Text{
			"en": {
				Label:       "Certificate file",
				Description: "PEM certificate the server presents; empty uses the one created in the data directory.",
				Why:         "Browsers and devices check this certificate. The self signed one that theserver creates on its first start works, but every browser warns about it. Enter a certificate from your own or a public authority here, together with its key file, and the warning goes away.",
			},
			"de": {
				Label:       "Zertifikatsdatei",
				Description: "PEM-Zertifikat, das der Server vorzeigt; leer nimmt das im Datenverzeichnis erzeugte.",
				Why:         "Browser und Geräte prüfen dieses Zertifikat. Das selbst signierte, das theserver beim ersten Start erzeugt, funktioniert, aber jeder Browser warnt davor. Tragen Sie hier ein Zertifikat Ihrer eigenen oder einer öffentlichen Zertifizierungsstelle ein, zusammen mit seiner Schlüsseldatei, dann verschwindet die Warnung.",
			},
		},
	},
	{
		Key: "tls.key_file", Section: "tls", Kind: Path, Default: "", Empty: true,
		Restart: true,
		Text: map[string]Text{
			"en": {
				Label:       "Key file",
				Description: "PEM private key of the certificate file; set both or neither.",
				Why:         "The key belongs to the certificate above and must stay secret. Both fields are set together or both stay empty; one without the other keeps the server from starting.",
			},
			"de": {
				Label:       "Schlüsseldatei",
				Description: "Privater PEM-Schlüssel zur Zertifikatsdatei; beide setzen oder keinen.",
				Why:         "Der Schlüssel gehört zum Zertifikat darüber und muss geheim bleiben. Beide Felder werden gemeinsam gesetzt oder bleiben beide leer; eines ohne das andere verhindert den Start des Servers.",
			},
		},
	},
	{
		Key: "store.busy_timeout_ms", Section: "store", Kind: Duration, Default: int64(5000),
		Unit: "ms", Min: bound(0), Max: bound(600000), Restart: true,
		Text: map[string]Text{
			"en": {
				Label:       "Database wait time",
				Description: "How long a database write waits for a lock before it gives up.",
				Why:         "Only one write reaches the database at a time. Commands on the server, such as adding a device while the server runs, wait at most this long. Raise it on slow storage; 0 means not to wait at all.",
			},
			"de": {
				Label:       "Wartezeit der Datenbank",
				Description: "Wie lange ein Schreibzugriff auf eine Sperre der Datenbank wartet, bevor er aufgibt.",
				Why:         "Es schreibt immer nur ein Zugriff zugleich in die Datenbank. Befehle auf dem Server, etwa ein neues Gerät bei laufendem Server, warten höchstens so lange. Erhöhen Sie den Wert bei langsamem Speicher; 0 heißt, gar nicht zu warten.",
			},
		},
	},
	{
		Key: "link.ack_interval_ms", Section: "link", Kind: Duration, Default: int64(100),
		Unit: "ms", Min: bound(1), Max: bound(10000),
		Text: map[string]Text{
			"en": {
				Label:       "Acknowledgement interval",
				Description: "At the latest after this time, received events are stored and acknowledged.",
				Why:         "A target keeps every event until theserver acknowledges it. A short interval frees the target's journal sooner, a longer one stores more events per write. 100 ms suits most venues. A change applies to the next batch.",
			},
			"de": {
				Label:       "Bestätigungsintervall",
				Description: "Spätestens nach dieser Zeit werden empfangene Ereignisse gespeichert und bestätigt.",
				Why:         "Ein Ziel behält jedes Ereignis, bis theserver es bestätigt. Ein kurzes Intervall entlastet das Journal des Ziels früher, ein längeres speichert mehr Ereignisse pro Schreibvorgang. 100 ms passen für die meisten Standorte. Eine Änderung gilt ab dem nächsten Stapel.",
			},
		},
	},
	{
		Key: "link.ack_batch", Section: "link", Kind: Int, Default: int64(32),
		Min: bound(1), Max: bound(1024),
		Text: map[string]Text{
			"en": {
				Label:       "Acknowledgement batch",
				Description: "Number of received events that are stored and acknowledged together.",
				Why:         "When this many events have arrived, theserver stores them at once, even before the interval above has passed. Venues with many targets profit from larger batches; 32 is a good start. A change applies to the next batch.",
			},
			"de": {
				Label:       "Bestätigungsmenge",
				Description: "Anzahl empfangener Ereignisse, die gemeinsam gespeichert und bestätigt werden.",
				Why:         "Sind so viele Ereignisse eingetroffen, speichert theserver sie sofort, auch bevor das Intervall darüber abgelaufen ist. Standorte mit vielen Zielen profitieren von größeren Mengen; 32 ist ein guter Anfang. Eine Änderung gilt ab dem nächsten Stapel.",
			},
		},
	},
	{
		Key: "link.ping_interval_s", Section: "link", Kind: Duration, Default: int64(15),
		Unit: "s", Min: bound(1), Max: bound(3600),
		Text: map[string]Text{
			"en": {
				Label:       "Ping interval",
				Description: "Time between two keepalive pings to a connected device.",
				Why:         "theserver pings every connected device to notice a lost connection. A shorter interval notices it sooner and costs a little more traffic. A change applies from each device's next ping.",
			},
			"de": {
				Label:       "Ping-Intervall",
				Description: "Zeit zwischen zwei Pings an ein verbundenes Gerät.",
				Why:         "theserver pingt jedes verbundene Gerät an, um eine abgerissene Verbindung zu bemerken. Ein kürzeres Intervall bemerkt sie früher und kostet etwas mehr Datenverkehr. Eine Änderung gilt ab dem nächsten Ping des jeweiligen Geräts.",
			},
		},
	},
	{
		Key: "link.pong_timeout_s", Section: "link", Kind: Duration, Default: int64(10),
		Unit: "s", Min: bound(1), Max: bound(600),
		Text: map[string]Text{
			"en": {
				Label:       "Ping answer time",
				Description: "Time a device has to answer a ping before it counts as offline.",
				Why:         "A device that does not answer in time is disconnected and shown as offline. It connects again and delivers what it recorded in the meantime. Raise the value on a weak WiFi.",
			},
			"de": {
				Label:       "Antwortzeit auf Pings",
				Description: "Zeit, die ein Gerät für die Antwort auf einen Ping hat, bevor es als offline gilt.",
				Why:         "Ein Gerät, das nicht rechtzeitig antwortet, wird getrennt und als offline angezeigt. Es verbindet sich neu und liefert nach, was es inzwischen aufgezeichnet hat. Erhöhen Sie den Wert bei schwachem WLAN.",
			},
		},
	},
	{
		Key: "link.hello_timeout_s", Section: "link", Kind: Duration, Default: int64(5),
		Unit: "s", Min: bound(1), Max: bound(600),
		Text: map[string]Text{
			"en": {
				Label:       "Greeting time",
				Description: "Time a new device connection has to introduce itself.",
				Why:         "A new connection must send its greeting, the hello, within this time, or it is closed. This keeps half open connections from piling up. A change applies to connections that open after it.",
			},
			"de": {
				Label:       "Zeit für die Begrüßung",
				Description: "Zeit, in der sich eine neue Geräteverbindung vorstellen muss.",
				Why:         "Eine neue Verbindung muss ihre Begrüßung, das Hello, innerhalb dieser Zeit senden, sonst wird sie geschlossen. So sammeln sich keine halb offenen Verbindungen an. Eine Änderung gilt für Verbindungen, die danach entstehen.",
			},
		},
	},
	{
		Key: "log.level", Section: "log", Kind: Enum, Default: "info",
		Enum: []string{"debug", "info", "warn", "error"}, Flag: "log-level",
		Text: map[string]Text{
			"en": {
				Label:       "Log level",
				Description: "Lowest level of the messages written to the log.",
				Why:         "info records what an operator needs: starts, logins, changes, devices coming and going. debug adds many details for troubleshooting; warn and error keep only problems.",
			},
			"de": {
				Label:       "Protokollstufe",
				Description: "Niedrigste Stufe der Meldungen, die ins Protokoll geschrieben werden.",
				Why:         "info hält fest, was ein Betreiber braucht: Starts, Anmeldungen, Änderungen, kommende und gehende Geräte. debug ergänzt viele Einzelheiten für die Fehlersuche; warn und error behalten nur Probleme.",
			},
		},
	},
	{
		Key: "log.format", Section: "log", Kind: Enum, Default: "text",
		Enum: []string{"text", "json"}, Restart: true,
		Text: map[string]Text{
			"en": {
				Label:       "Log format",
				Description: "Plain text for reading, JSON for programs that collect logs.",
				Why:         "Text lines are easy to read in a console window. JSON lines suit programs that collect, store and search logs.",
			},
			"de": {
				Label:       "Protokollformat",
				Description: "Klartext zum Lesen, JSON für Programme, die Protokolle sammeln.",
				Why:         "Textzeilen lesen sich leicht in einem Konsolenfenster. JSON-Zeilen passen zu Programmen, die Protokolle sammeln, speichern und durchsuchen.",
			},
		},
	},
	{
		Key: "admin.language", Section: "admin", Kind: Enum, Default: "en",
		Enum: []string{"en", "de"},
		Text: map[string]Text{
			"en": {
				Label:       "Admin language",
				Description: "Language of the admin pages for a login that has not chosen one.",
				Why:         "Every operator can switch the language at the top of the page, and that choice lasts for the login. This setting is the language a new login starts with: en for English, de for German.",
			},
			"de": {
				Label:       "Sprache der Verwaltung",
				Description: "Sprache der Verwaltungsseiten für eine Anmeldung, die noch keine gewählt hat.",
				Why:         "Jeder Betreiber kann die Sprache oben auf der Seite umschalten, und diese Wahl gilt für die Anmeldung. Diese Einstellung ist die Sprache, mit der eine neue Anmeldung beginnt: en für Englisch, de für Deutsch.",
			},
		},
	},
	{
		Key: "admin.session_hours", Section: "admin", Kind: Duration, Default: int64(12),
		Unit: "h", Min: bound(1), Max: bound(720),
		Text: map[string]Text{
			"en": {
				Label:       "Login duration",
				Description: "How long an admin login lasts before the token is asked for again.",
				Why:         "A shorter time protects a computer that several people use; a longer one saves logging in again on a machine only staff can reach. A change applies to logins made after it.",
			},
			"de": {
				Label:       "Dauer der Anmeldung",
				Description: "Wie lange eine Admin-Anmeldung gilt, bevor das Token erneut abgefragt wird.",
				Why:         "Eine kürzere Zeit schützt einen Rechner, den mehrere Personen nutzen; eine längere erspart das erneute Anmelden auf einem Gerät, das nur Personal erreicht. Eine Änderung gilt für Anmeldungen danach.",
			},
		},
	},
}
