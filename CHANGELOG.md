# Changelog

## [0.3.0] — 2026-11-16

### Added
- `fhj_timetable`: when and where classes are, from the FH timetable (`almaty.fh-joanneum.at`,
  not Moodle) — time, course, type, lecturer, group, room or MS-Teams, by day. The setup asks for
  the programme (e.g. `dat26`) and optional groups (`FHJ_TIMETABLE`, `FHJ_TIMETABLE_GROUPS`). It
  works without a Moodle token. The briefing shows today's and tomorrow's classes.
- Setup: asks whether to set up Claude Code, Codex or both when both are installed;
  `--timetable` / `--groups` (`-Timetable` / `-Groups`) answer the timetable questions up front,
  so an assistant can collect them in the chat and the terminal only asks for the FH login.
  It points out an old `moodle` registration left from before the rename.

### Fixed
- `moodle_deadlines` listed every unsubmitted assignment twice (once with "status unknown") and
  never knew a quiz's status: a calendar event's `instance` is the course-module id, not the
  activity id. Deadlines are now merged by course-module id; status is asked with the real
  activity id (looked up in the course contents for calendar-only quizzes). Personal date
  overrides from the calendar apply again.
- Deadlines: a quiz's opening time or an "expected completion" date no longer replaces the
  real due or close date.
- Setup: the Codex registration check could fail although Codex had saved the entry; it now
  also accepts the config file and shows what Codex said. The step counter starts with the
  right total.
- `moodle_download` with a new folder name without extension (`~/Desktop/pdp`) created a file
  named `pdp`; it now creates the folder.

### Security
- `moodle_download` saves only into the download folder or into Desktop, Documents or Downloads;
  hidden files and folders, PowerShell profile folders and Windows junctions are refused
  (symlinks resolved).
- The skills no longer pre-approve `moodle_download`, `Write`, `python3` or `open`: those steps
  ask first, since Moodle content is untrusted text.

### Changed
- Dates are shown in the computer's time zone (or `FHJ_TZ`) and always carry the zone
  (`17:15 CEST`). Before, they were Vienna time without a label, which misleads anyone working
  from another time zone.

## [0.2.1] — 2026-10-03

### Fixed
- `moodle_download` with the default destination saved the file as `~/Downloads/moodle`
  instead of into that folder when the folder did not exist yet.
- A destination starting with `~` fails with a clear error when the home directory is unknown,
  instead of creating a folder named `~` in the working directory. On Windows `~\…` is
  expanded too.

## [0.2.0] — 2026-10-03

### Added
- `moodle_whats_new`: everything that changed in the last N days in one call. It covers new or
  updated course files (grouped by course, with section and links), announcements and new
  grades. The briefing and dashboard skills use it.
- Dashboard:
  - the calendar has labels, a legend and short labels on phones;
  - new section "New material";
  - grades show teacher feedback and course totals;
  - the summary cards are clickable.
- Codex support: `setup.sh` / `setup.ps1` register with every installed client. The skills are
  linked to `~/.agents/skills` for Codex.

### Changed
- Deadlines:
  - two lines per item (what, then when and status);
  - "Overdue" and "Coming up" sections;
  - a neutral ⬜ before the deadline, ❌ only when overdue;
  - ⏰ for open work due within 48 h.
- Grades: Moodle's localised "87,50" is shown as "87.5" next to the maximum and the percentage.
- `setup.sh`: numbered, coloured steps and a short "what to ask first" summary.

### Fixed
- Error messages redact every field of a Moodle error object, not only the message.
- Error snippets cannot form the token through `%q` escapes. Both were found by fuzzing.

## [0.1.0] — 2026-10-03

First release.

- Read-only MCP server for Moodle 4.5 with 8 tools:
  - deadlines with submission and quiz status;
  - announcements;
  - courses and course contents;
  - search, including inside PDF, docx, pptx, ipynb and zip;
  - grades with feedback;
  - download;
  - diagnostics.
- Seven Claude Code skills, including a visual HTML dashboard.
- `setup.sh` (macOS/Linux, any shell) and `setup.ps1` (Windows) get the token from
  `login/token.php`, verify it and register the server. Without Go they download a
  checksum-verified release binary.
- CI on Linux, macOS and Windows; release binaries for six platforms.

[0.2.1]: https://github.com/armenbarseghyan/fhj-study-mcp/releases/tag/v0.2.1
[0.2.0]: https://github.com/armenbarseghyan/fhj-study-mcp/releases/tag/v0.2.0
[0.1.0]: https://github.com/armenbarseghyan/fhj-study-mcp/releases/tag/v0.1.0
