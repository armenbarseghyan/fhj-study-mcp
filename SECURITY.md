# Security

fhj-study-mcp holds a personal Moodle token, so its security properties are deliberate:

- **Read-only.** The client refuses every Moodle function outside an allowlist of getters
  (`internal/moodle/allowlist.go`); nothing that submits, posts, enrols or changes data is in
  the code.
- **The token stays local.** It is read only from `MOODLE_TOKEN`. It is redacted in logs,
  errors and tool output, and is never sent anywhere but the configured Moodle. File downloads
  are refused for URLs outside that Moodle, and file links in answers are browser links
  without the token.
- **Downloads stay in ordinary folders.** `moodle_download` writes only into the download folder
  or into Desktop, Documents or Downloads (an allowlist: no hidden or PowerShell profile folders,
  symlinks resolved, Windows junctions refused), and the skills ask before downloading or writing files.
- **The timetable is a separate, public source.** `fhj_timetable` asks the fixed URL
  `https://almaty.fh-joanneum.at/stundenplan/json.php` with your search and dates only; it has its
  own HTTP client and never sees the Moodle token.
- **HTTP mode** refuses non-loopback addresses without `MCP_HTTP_TOKEN` and rejects DNS
  rebinding and cross-origin browser requests.
- **The setup scripts** send the password only to `<MOODLE_URL>/login/token.php`, via stdin
  rather than the command line, and never store it. Release binaries are checked against
  `checksums.txt` before use.

## Reporting a vulnerability

Please report it privately through GitHub: *Security → Report a vulnerability* on this
repository. Don't open a public issue. Never include a real token in a report; if one
leaked, see "Keep it secret" in [docs/STUDENT-SETUP.md](docs/STUDENT-SETUP.md).
