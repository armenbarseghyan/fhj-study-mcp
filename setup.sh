#!/usr/bin/env bash
# setup.sh — build fhj-study-mcp, get your Moodle token and register the server
# with Claude Code and/or Codex. Safe to re-run: it replaces an existing
# registration.
#
#   ./setup.sh                 interactive (asks for your FH login once)
#   MOODLE_TOKEN=… ./setup.sh  use a token you already have
#   ./setup.sh --codex         only Codex (--claude: only Claude Code;
#                              default: every one that is installed)
#   ./setup.sh --no-skills     don't install the skills
#   ./setup.sh --timetable dat26 --groups "Gruppe 2"
#                              class timetable without asking (--timetable ""
#                              skips it; FHJ_TIMETABLE/_GROUPS work too)
#
# Your password is sent only to $MOODLE_URL/login/token.php (the endpoint the
# official Moodle app uses), through stdin so it never shows up in the process
# list, and is not stored. The token ends up only in Claude Code's config.
# See docs/STUDENT-SETUP.md for what each step does and for troubleshooting.

set -euo pipefail

REPO="armenbarseghyan/fhj-study-mcp"
MOODLE_URL="${MOODLE_URL:-https://moodle.fh-joanneum.at}"
MOODLE_URL="${MOODLE_URL%/}"
SERVER_NAME="${FHJ_MCP_NAME:-fhj}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN="$ROOT/bin/fhj-study-mcp"
SKILLS="ask"
CLIENTS="" # "claude", "codex" or both; empty: every installed one
TIMETABLE_ASKED="" # set by --timetable, even when empty: don't ask

usage() {
	sed -n '2,18p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
}

while [ $# -gt 0 ]; do
	case "$1" in
	--skills) SKILLS="yes" ;;
	--no-skills) SKILLS="no" ;;
	--claude | --codex) CLIENTS="$CLIENTS ${1#--}" ;;
	--timetable | --groups)
		[ $# -ge 2 ] || { usage >&2; exit 2; }
		if [ "$1" = "--timetable" ]; then FHJ_TIMETABLE="$2" TIMETABLE_ASKED=1; else FHJ_TIMETABLE_GROUPS="$2"; fi
		shift
		;;
	--url)
		[ $# -ge 2 ] || { usage >&2; exit 2; }
		MOODLE_URL="${2%/}"
		shift
		;;
	-h | --help)
		usage
		exit 0
		;;
	*)
		printf 'unknown option: %s\n\n' "$1" >&2
		usage >&2
		exit 2
		;;
	esac
	shift
done

# Colours only on a terminal, and never with NO_COLOR set (https://no-color.org).
if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
	B=$'\033[1m' DIM=$'\033[2m' GREEN=$'\033[32m' RED=$'\033[31m' CYAN=$'\033[36m' R=$'\033[0m'
else
	B="" DIM="" GREEN="" RED="" CYAN="" R=""
fi
STEP=0
TOTAL=6 # updated once the clients are known
step() {
	STEP=$((STEP + 1))
	printf '\n%s[%d/%d]%s %s%s%s\n' "$CYAN" "$STEP" "$TOTAL" "$R" "$B" "$*" "$R"
}
ok() { printf '    %s✓%s %s\n' "$GREEN" "$R" "$*"; }
note() { printf '    %s%s%s\n' "$DIM" "$*" "$R"; }
die() {
	printf '\n    %s✗ %s%s\n' "$RED" "$*" "$R" >&2
	exit 1
}
interactive() { [ -t 0 ]; }

# json_field NAME — the string value of "NAME" in the JSON on stdin, compact or
# pretty-printed (values here contain no quotes).
# Anchored on the opening quote, so "token" never matches "privatetoken".
json_field() {
	sed -n "s/.*\"$1\"[[:space:]]*:[[:space:]]*\"\\([^\"]*\\)\".*/\\1/p" | head -n 1
}

# ---------------------------------------------------------------------------
printf '%sfhj-study-mcp setup%s — connects your FH JOANNEUM Moodle to your AI assistant.\n' "$B" "$R"
note "Read-only: it can read your courses, never change anything in Moodle."
# Pick the clients first: the step total depends on them.
if [ -z "$CLIENTS" ]; then
	for c in claude codex; do
		if command -v "$c" >/dev/null; then CLIENTS="$CLIENTS $c"; fi
	done
	[ -n "$CLIENTS" ] || die "neither Claude Code ('claude') nor Codex ('codex') is on your PATH. Install one (https://claude.com/claude-code or https://developers.openai.com/codex) and run this script again."
	if [ "$CLIENTS" = " claude codex" ] && interactive; then
		printf '\n'
		read -r -p "    Both Claude Code and Codex are installed. Set up for: 1) Claude Code  2) Codex  3) both [3]: " answer
		case "$answer" in
		1) CLIENTS=" claude" ;;
		2) CLIENTS=" codex" ;;
		esac
	fi
else
	for c in $CLIENTS; do
		command -v "$c" >/dev/null || die "'$c' is not on your PATH."
	done
fi
# prerequisites, build, token, check, one registration per client, skills
TOTAL=5
for c in $CLIENTS; do TOTAL=$((TOTAL + 1)); done
step "Checking prerequisites"
command -v curl >/dev/null || die "curl is required."
CLIENT_NAMES="$(printf '%s' "$CLIENTS" | sed 's/ claude/ Claude Code/; s/ codex/ Codex/; s/^ //; s/Code Codex/Code and Codex/')"
ok "setting up for $CLIENT_NAMES"

# ---------------------------------------------------------------------------
step "Building the server"
mkdir -p "$ROOT/bin"
if command -v go >/dev/null; then
	version="$(git -C "$ROOT" describe --tags --always --dirty 2>/dev/null || echo dev)"
	(cd "$ROOT" && go build -trimpath -ldflags "-s -w -X main.version=$version" -o "$BIN" ./cmd/fhj-study-mcp) ||
		die "go build failed (the project needs Go 1.26; Go 1.21 or newer downloads it automatically)."
	ok "built $BIN ($version)"
else
	os="$(uname -s | tr '[:upper:]' '[:lower:]')"
	arch="$(uname -m)"
	case "$arch" in
	x86_64 | amd64) arch="amd64" ;;
	arm64 | aarch64) arch="arm64" ;;
	*) die "no prebuilt binary for $arch — install Go from https://go.dev/dl and run this script again." ;;
	esac
	case "$os" in darwin | linux) ;; *) die "no prebuilt binary for $os — install Go and run this script again." ;; esac
	asset="fhj-study-mcp-$os-$arch"
	base="https://github.com/$REPO/releases/latest/download"
	printf '    Go is not installed; downloading the latest release (%s)\n' "$asset"
	tmp="$(mktemp -d)"
	trap 'rm -rf "$tmp"' EXIT
	curl -fsSL "$base/$asset" -o "$tmp/$asset" || die "download of $base/$asset failed."
	curl -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt" || die "download of checksums.txt failed."
	expected="$(grep " $asset\$" "$tmp/checksums.txt" | cut -d' ' -f1)"
	if command -v shasum >/dev/null; then
		actual="$(shasum -a 256 "$tmp/$asset" | cut -d' ' -f1)"
	else
		actual="$(sha256sum "$tmp/$asset" | cut -d' ' -f1)"
	fi
	if [ -z "$expected" ] || [ "$expected" != "$actual" ]; then
		die "checksum mismatch for $asset — not installing it."
	fi
	mv "$tmp/$asset" "$BIN"
	chmod +x "$BIN"
	ok "downloaded and verified $BIN"
fi
"$BIN" -version >/dev/null 2>&1 || die "$BIN does not run."

# ---------------------------------------------------------------------------
step "Getting your Moodle token"
token="${MOODLE_TOKEN:-}"
if [ -n "$token" ]; then
	ok "using MOODLE_TOKEN from the environment"
else
	interactive || die "no terminal to ask for your login; run with MOODLE_TOKEN=<token> instead."
	note "Your FH login goes once to $MOODLE_URL/login/token.php and is not saved."
	note "Type it (don't paste several lines at once). The password stays hidden as you type."
	read -r -p "    FH username: " username
	read -r -s -p "    FH password (hidden): " password
	printf '\n'
	if [ -z "$username" ] || [ -z "$password" ]; then
		die "username and password are required."
	fi
	response="$(printf '%s' "$password" | curl -sS -X POST "$MOODLE_URL/login/token.php" \
		--data-urlencode "username=$username" \
		--data-urlencode "password@-" \
		--data-urlencode "service=moodle_mobile_app")" || die "could not reach $MOODLE_URL."
	unset password
	token="$(printf '%s' "$response" | json_field token)"
	if [ -z "$token" ]; then
		code="$(printf '%s' "$response" | json_field errorcode)"
		msg="$(printf '%s' "$response" | json_field error)"
		case "$code" in
		invalidlogin) die "Moodle rejected the username or password ($code)." ;;
		enablewsdescription | servicenotavailable | servicerequireslogin)
			die "Moodle's mobile web service is not available for your account ($code: $msg). Ask the FH helpdesk." ;;
		*) die "Moodle did not return a token (${code:-no errorcode}: ${msg:-unexpected answer})." ;;
		esac
	fi
	ok "token received"
fi
# The API key is "token" (32 hex characters). "privatetoken" is something else;
# token+privatetoken pasted together is 64 characters and fails as invalidtoken.
printf '%s' "$token" | grep -Eq '^[0-9a-f]{32}$' ||
	die "that is not a Moodle API token (expected 32 hex characters, got ${#token}). Use only the \"token\" value, not \"privatetoken\"."

# ---------------------------------------------------------------------------
step "Checking the token against Moodle"
site="$(printf '%s' "$token" | curl -sS -X POST "$MOODLE_URL/webservice/rest/server.php" \
	--data-urlencode "wstoken@-" \
	--data-urlencode "wsfunction=core_webservice_get_site_info" \
	--data-urlencode "moodlewsrestformat=json")" || die "could not reach $MOODLE_URL."
code="$(printf '%s' "$site" | json_field errorcode)"
[ -z "$code" ] || die "Moodle refused the token ($code). Get a new one by running this script again without MOODLE_TOKEN."
fullname="$(printf '%s' "$site" | json_field fullname)"
username_ws="$(printf '%s' "$site" | json_field username)"
ok "signed in as ${fullname:-?} (${username_ws:-?})"

# Class timetable (fhj_timetable): the search and the groups go into the
# registration. Optional; it works without Moodle data.
TIMETABLE="${FHJ_TIMETABLE:-}"
TIMETABLE_GROUPS="${FHJ_TIMETABLE_GROUPS:-}"
if [ -z "$TIMETABLE" ] && [ -z "$TIMETABLE_ASKED" ] && interactive; then
	read -r -p "    Timetable: your programme code and year, e.g. dat26 (Enter to skip): " TIMETABLE
	if [ -n "$TIMETABLE" ] && [ -z "$TIMETABLE_GROUPS" ]; then
		read -r -p "    Your groups, comma-separated, e.g. Gruppe 2 (Enter to show all groups): " TIMETABLE_GROUPS
	fi
fi
claude_env=() codex_env=()
if [ -n "$TIMETABLE" ]; then
	claude_env+=(-e "FHJ_TIMETABLE=$TIMETABLE") codex_env+=(--env "FHJ_TIMETABLE=$TIMETABLE")
	ok "timetable: $TIMETABLE${TIMETABLE_GROUPS:+ ($TIMETABLE_GROUPS)}"
fi
if [ -n "$TIMETABLE" ] && [ -n "$TIMETABLE_GROUPS" ]; then
	claude_env+=(-e "FHJ_TIMETABLE_GROUPS=$TIMETABLE_GROUPS") codex_env+=(--env "FHJ_TIMETABLE_GROUPS=$TIMETABLE_GROUPS")
fi

# Both clients start MCP servers with a reduced PATH: register the absolute path.
not_connecting() {
	die "'$SERVER_NAME' is registered with $1 but does not connect. Run the binary by hand to see why:
      MOODLE_URL=$MOODLE_URL MOODLE_TOKEN=<token> $BIN
    and read the error on stderr (see docs/STUDENT-SETUP.md, Troubleshooting)."
}

# Before the rename the server was registered as "moodle". Only point it out:
# the name may belong to something else on this machine.
LEGACY_NAME="moodle"
legacy_hint() {
	[ "$SERVER_NAME" != "$LEGACY_NAME" ] || return 0
	"$1" mcp get "$LEGACY_NAME" >/dev/null 2>&1 || return 0
	note "A server named '$LEGACY_NAME' is registered too. If it is the old moodle-mcp, remove it,"
	note "or every Moodle tool appears twice:  $2"
}

register_claude() {
	step "Registering with Claude Code"
	if claude mcp get "$SERVER_NAME" >/dev/null 2>&1; then
		claude mcp remove "$SERVER_NAME" -s user >/dev/null 2>&1 || true
		ok "removed the previous '$SERVER_NAME' registration"
	fi
	claude mcp add "$SERVER_NAME" -s user \
		-e "MOODLE_URL=$MOODLE_URL" \
		-e "MOODLE_TOKEN=$token" \
		${claude_env[@]+"${claude_env[@]}"} \
		-- "$BIN" >/dev/null
	# "claude mcp get" starts the server and reports whether it connects.
	# Capture first: "… | grep -q" under pipefail fails when grep stops reading
	# early and the client dies of SIGPIPE.
	out="$(claude mcp get "$SERVER_NAME" 2>/dev/null || true)"
	case "$out" in
	*Connected*) ok "'$SERVER_NAME' is registered and connects" ;;
	*) not_connecting "Claude Code" ;;
	esac
	legacy_hint claude "claude mcp remove $LEGACY_NAME"
}

register_codex() {
	step "Registering with Codex"
	# "codex mcp add" replaces an existing entry of the same name.
	codex mcp add "$SERVER_NAME" \
		--env "MOODLE_URL=$MOODLE_URL" \
		--env "MOODLE_TOKEN=$token" \
		${codex_env[@]+"${codex_env[@]}"} \
		-- "$BIN" >/dev/null
	# "codex mcp get" shows the configuration only; it doesn't start the server.
	# Accept either view of the registration: what "codex mcp get" prints, or
	# the config file it writes. On failure show what Codex said.
	out="$(codex mcp get "$SERVER_NAME" 2>&1 || true)"
	config="${CODEX_HOME:-$HOME/.codex}/config.toml"
	case "$out" in
	*"command: $BIN"*) ok "'$SERVER_NAME' is registered (~/.codex/config.toml)" ;;
	*)
		if [ -f "$config" ] && grep -qF "command = \"$BIN\"" "$config"; then
			ok "'$SERVER_NAME' is registered ($config)"
		else
			die "Codex did not save the registration. 'codex mcp get $SERVER_NAME' said:
$(printf '%s\n' "$out" | sed 's/^/      /')"
		fi
		;;
	esac
	legacy_hint codex "codex mcp remove $LEGACY_NAME"
}

# ---------------------------------------------------------------------------
for c in $CLIENTS; do "register_$c"; done
unset token

# ---------------------------------------------------------------------------
step "Skills"
# Claude Code reads ~/.claude/skills, Codex ~/.agents/skills; both follow links.
skill_dirs=""
for c in $CLIENTS; do
	case "$c" in
	claude) skill_dirs="$skill_dirs $HOME/.claude/skills" ;;
	codex) skill_dirs="$skill_dirs $HOME/.agents/skills" ;;
	esac
done
if [ "$SKILLS" = "ask" ]; then
	if interactive; then
		read -r -p "    Install the moodle skills (recommended)? [Y/n] " answer
		case "$answer" in [nN]*) SKILLS="no" ;; *) SKILLS="yes" ;; esac
	else
		SKILLS="yes"
	fi
fi
if [ "$SKILLS" = "yes" ]; then
	for target in $skill_dirs; do
		mkdir -p "$target"
		for dir in "$ROOT"/skills/*/; do
			ln -sfn "${dir%/}" "$target/$(basename "$dir")"
		done
		ok "skills linked into ~/${target#"$HOME"/} (they update with git pull)"
	done
else
	ok "skipped"
fi

cat <<EOF

${GREEN}${B}✓ All set.${R}

  ${B}1.${R} Restart ${CLIENT_NAMES}.
  ${B}2.${R} Ask: ${CYAN}"check my moodle connection"${R}
  ${B}3.${R} Then try: ${CYAN}"what's due this week?"${R} · ${CYAN}"anything new?"${R} · ${CYAN}"make a dashboard of my next two weeks"${R}

  Classes: ${CYAN}"when are my classes this week?"${R}
  Once per semester: ${CYAN}"set up my course profiles from the syllabi"${R}
  ${DIM}Help and troubleshooting: docs/STUDENT-SETUP.md${R}
EOF
