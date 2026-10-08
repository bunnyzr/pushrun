#!/bin/sh
# pushrun client (rendered server-side; do not edit — `git pushrun update`
# re-downloads it). Thin glue: a git push with extra headers plus a few HTTP
# convenience calls. All complexity lives server-side.
#
# Subcommands:
#   run (default) | sync | test | build   push HEAD and run the pipeline
#   start | stop | restart | rerun        act on the instance without pushing
#   status                                show the instance state
#   logs ls | tree | fetch <path> [-f]    business logs
#   doctor                                local + server connectivity checks
#   update                                re-download this client
#
# Flags (anywhere on the command line):
#   --instance NAME   instance to act on (default: default)
#   --project NAME    explicit project (when the origin repo is mounted by
#                     several projects, or only as a non-primary git node)
#   --server URL      override the configured server
#   --token TOKEN     override the configured token
#
# Environment overrides: PUSHRUN_PROJECT (same as --project), PUSHRUN_CONFIG,
# PUSHRUN_CACHE_DIR.

set -u

PUSHRUN_CLIENT_VERSION="{{.Version}}"
PUSHRUN_BAKED_SERVER="{{.ServerURL}}"

CONFIG_FILE="${PUSHRUN_CONFIG:-$HOME/.config/pushrun/config}"
CACHE_DIR="${PUSHRUN_CACHE_DIR:-$HOME/.local/share/pushrun}"
SELF="$CACHE_DIR/client.sh"

SERVER=""
TOKEN=""
INSTANCE="default"
PROJECT="${PUSHRUN_PROJECT:-}"
CURL_CONFIG=""
PUSH_LOG=""

# cleanup removes the temp files holding the curl auth config and the push
# output capture.
cleanup() {
	[ -z "$CURL_CONFIG" ] || rm -f "$CURL_CONFIG"
	[ -z "$PUSH_LOG" ] || rm -f "$PUSH_LOG" "$PUSH_LOG.rc"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM

die() {
	echo "pushrun: $*" >&2
	exit 1
}

usage() {
	cat <<'EOF'
usage: git pushrun [--instance NAME] [--project NAME] [--server URL] [--token TOKEN] [COMMAND]

commands:
  run (default)   push HEAD and run the full pipeline
  sync            push and assemble only (no pipeline steps)
  build|test      push and run steps without the background service
  start|stop|restart|rerun
                  act on the instance without pushing code
  status          show the instance state
  logs ls|tree    list business log files
  logs fetch P [-f]  print a log file (last 200 lines; -f follows)
  doctor          check tools, config, alias, server connectivity
  update          re-download the client from the server
EOF
}

# json_field extracts the FIRST string value of a flat JSON field on a line.
# The leftmost match is deliberate: the runs list is newest-first and the
# record's status precedes its steps, so the first "status" on the line is
# the latest run's. (A greedy sed pattern would capture the LAST one.)
json_field() {
	awk -v f="\"$1\"" '{
		i = index($0, f)
		if (i == 0) next
		rest = substr($0, i + length(f))
		if (match(rest, /^[[:space:]]*:[[:space:]]*"[^"]*"/)) {
			v = substr(rest, RSTART, RLENGTH)
			gsub(/^[[:space:]]*:[[:space:]]*"/, "", v)
			sub(/"$/, "", v)
			print v
			exit
		}
	}'
}

# --- configuration ---------------------------------------------------------

# pre_scan pulls --server/--token out of the raw arguments so the version
# handshake can run before full parsing (it re-execs with "$@" intact).
pre_scan() {
	while [ $# -gt 0 ]; do
		case $1 in
		--server | --token)
			if [ $# -ge 2 ]; then
				[ "$1" = "--server" ] && SERVER=$2 || TOKEN=$2
				shift 2
			else
				shift
			fi
			;;
		--server=*) SERVER=${1#*=}; shift ;;
		--token=*) TOKEN=${1#*=}; shift ;;
		*) shift ;;
		esac
	done
}

# load_config reads server=/token= lines; flags already set win.
load_config() {
	[ -f "$CONFIG_FILE" ] || return 0
	while IFS='=' read -r _k _v; do
		case $_k in
		server) [ -n "$SERVER" ] || SERVER=$_v ;;
		token) [ -n "$TOKEN" ] || TOKEN=$_v ;;
		esac
	done <"$CONFIG_FILE"
}

require_config() {
	command -v curl >/dev/null 2>&1 || die "curl is required but not installed"
	[ -n "$SERVER" ] && [ -n "$TOKEN" ] ||
		die "no server/token configured — install with: curl -H 'Authorization: Bearer <token>' $PUSHRUN_BAKED_SERVER/install.sh | sh"
}

# --- version handshake -----------------------------------------------------

# handshake compares the baked-in version with the server's; on mismatch it
# re-downloads client.sh into the cache and re-execs once.
handshake() {
	[ -z "${PUSHRUN_SELF_UPDATED:-}" ] || return 0
	[ -n "$SERVER" ] && [ -n "$TOKEN" ] || return 0
	curl_auth_config
	_v=$(curl -fsS --max-time 5 -K "$CURL_CONFIG" \
		"$SERVER/api/v1/version" 2>/dev/null | json_field version) || return 0
	[ -n "$_v" ] || return 0
	[ "$_v" = "$PUSHRUN_CLIENT_VERSION" ] && return 0
	echo "pushrun: server runs $_v, client is $PUSHRUN_CLIENT_VERSION — updating $SELF" >&2
	mkdir -p "$CACHE_DIR"
	if curl -fsS -K "$CURL_CONFIG" "$SERVER/client.sh" -o "$SELF.tmp.$$"; then
		chmod +x "$SELF.tmp.$$"
		mv "$SELF.tmp.$$" "$SELF"
		PUSHRUN_SELF_UPDATED=1 exec sh "$SELF" "$@"
	fi
	rm -f "$SELF.tmp.$$"
	echo "pushrun: self-update failed; continuing with $PUSHRUN_CLIENT_VERSION" >&2
}

# --- HTTP ------------------------------------------------------------------

# curl_auth_config writes the Authorization header into a 0600 curl config
# file (once per invocation) so the token never appears in curl's argv,
# where `ps` would show it to other local users.
curl_auth_config() {
	[ -z "$CURL_CONFIG" ] || return 0
	CURL_CONFIG=$(mktemp "${TMPDIR:-/tmp}/pushrun-curl.XXXXXX") || die "mktemp failed"
	chmod 600 "$CURL_CONFIG"
	printf 'header = "Authorization: Bearer %s"\n' "$TOKEN" >"$CURL_CONFIG"
}

# api_request METHOD PATH [BODY] prints the response body of a 2xx and dies
# (in the current or subshell context) otherwise.
api_request() {
	_m=$1
	_p=$2
	_d=${3:-}
	curl_auth_config
	_out=$(mktemp "${TMPDIR:-/tmp}/pushrun-api.XXXXXX") || die "mktemp failed"
	_u=$SERVER$_p
	if [ -n "$_d" ]; then
		_code=$(curl -sS -o "$_out" -w '%{http_code}' -X "$_m" \
			-K "$CURL_CONFIG" \
			-H "Content-Type: application/json" \
			--data "$_d" "$_u")
	else
		_code=$(curl -sS -o "$_out" -w '%{http_code}' -X "$_m" \
			-K "$CURL_CONFIG" "$_u")
	fi || {
		rm -f "$_out"
		die "cannot reach $_u"
	}
	case $_code in
	2*)
		cat "$_out"
		rm -f "$_out"
		;;
	401)
		rm -f "$_out"
		die "401 unauthorized — the server rejected the token; check $CONFIG_FILE"
		;;
	*)
		sed 's/^/pushrun: server: /' "$_out" >&2
		rm -f "$_out"
		die "$_m $_p failed (HTTP $_code)"
		;;
	esac
}

# --- git repo context ------------------------------------------------------

require_repo() {
	git rev-parse --git-dir >/dev/null 2>&1 || die "not in a git repository"
}

# repo_identity reduces remote.origin.url to the canonical "host/path" repo
# identity that push URLs and server-side project matching key on: scheme and
# userinfo are dropped, the host (including port) is kept, scp-like
# [user@]host:path syntax is normalized to host/path, and a trailing ".git" is
# stripped. A local absolute path keeps its first segment as the host. This
# mirrors the server's normalization of repo mount params.
repo_identity() {
	_url=$(git config --get remote.origin.url) ||
		die "no remote.origin.url; cannot derive the repo identity (add an origin remote)"
	_u=${_url%/}
	case $_u in
	*://*)
		# scheme://[user@]host[:port]/path
		_u=${_u#*://}
		_host=${_u%%/*}
		_u=${_host#*@}/${_u#*/}
		;;
	*:*)
		# scp-like [user@]host:path (everything after the first colon is the
		# path — scp syntax has no port notation)
		_host=${_u%%:*}
		_u=${_host#*@}/${_u#*:}
		;;
	esac
	_u=${_u#/}
	_u=${_u%.git}
	if [ -z "$_u" ] || [ "${_u#*/}" = "$_u" ]; then
		die "cannot derive a host/path repo identity from remote.origin.url \"$_url\""
	fi
	printf '%s\n' "$_u"
}

# urlencode_path encodes just enough for a query parameter. Identity path
# segments may contain "+", "&", "=" (e.g. local paths like /Users/a+b), all
# of which are significant in a query string and must be escaped.
urlencode_path() {
	printf '%s' "$1" | sed \
		-e 's/%/%25/g' \
		-e 's/?/%3F/g' \
		-e 's/#/%23/g' \
		-e 's/ /%20/g' \
		-e 's/+/%2B/g' \
		-e 's/&/%26/g' \
		-e 's/=/\%3D/g'
}

# resolve_project answers which project this repo's commands act on, the same
# way the server matches a push: an explicit --project/PUSHRUN_PROJECT wins
# (asserted, validated server-side at push/action time); otherwise
# GET /api/v1/resolve matches the origin repo identity against the server's
# project definitions. Prints the project name; on failure the server's error
# is relayed and the function exits non-zero.
resolve_project() {
	if [ -n "$PROJECT" ]; then
		printf '%s\n' "$PROJECT"
		return 0
	fi
	_id=$(repo_identity) || exit 1
	_enc=$(urlencode_path "$_id")
	curl_auth_config
	_out=$(mktemp "${TMPDIR:-/tmp}/pushrun-resolve.XXXXXX") || die "mktemp failed"
	_code=$(curl -sS -o "$_out" -w '%{http_code}' -K "$CURL_CONFIG" \
		"$SERVER/api/v1/resolve?repo=$_enc") || {
		rm -f "$_out"
		die "cannot reach $SERVER"
	}
	case $_code in
	200) ;;
	401)
		rm -f "$_out"
		die "401 unauthorized — the server rejected the token; check $CONFIG_FILE"
		;;
	*)
		sed 's/^/pushrun: server: /' "$_out" >&2
		if grep -Eq 'project_required:|ambiguous_project:' "$_out"; then
			echo "pushrun: re-run with --project <name> to choose the project explicitly" >&2
		fi
		rm -f "$_out"
		exit 1
		;;
	esac
	_p=$(json_field project <"$_out")
	rm -f "$_out"
	[ -n "$_p" ] || die "server returned no project for repo $_id"
	printf '%s\n' "$_p"
}

current_branch() {
	_b=$(git symbolic-ref --quiet --short HEAD) || die "detached HEAD; check out a branch first"
	echo "$_b"
}

user_meta() {
	_u=$(git config --get user.email 2>/dev/null) || _u=unknown
	# display metadata only; keep it JSON- and header-safe
	echo "$_u" | tr -d '"\\'
}

# git_auth runs git with the token and pushrun metadata as extraHeaders
# scoped to the server host (git >= 2.31 env config; the token never lands
# in .git/config and is never sent to other remotes). An explicit --project
# rides as X-PushRun-Project, telling the pre-receive hook which project the
# push triggers when the repo identity alone is not enough.
git_auth() {
	_user=$1
	_action=$2
	shift 2
	(
		GIT_CONFIG_COUNT=4
		GIT_CONFIG_KEY_0="http.$SERVER/.extraHeader"
		GIT_CONFIG_VALUE_0="Authorization: Bearer $TOKEN"
		GIT_CONFIG_KEY_1="http.$SERVER/.extraHeader"
		GIT_CONFIG_VALUE_1="X-PushRun-User: $_user"
		GIT_CONFIG_KEY_2="http.$SERVER/.extraHeader"
		GIT_CONFIG_VALUE_2="X-PushRun-Action: $_action"
		GIT_CONFIG_KEY_3="http.$SERVER/.extraHeader"
		GIT_CONFIG_VALUE_3="X-PushRun-Instance: $INSTANCE"
		if [ -n "$PROJECT" ]; then
			GIT_CONFIG_COUNT=5
			GIT_CONFIG_KEY_4="http.$SERVER/.extraHeader"
			GIT_CONFIG_VALUE_4="X-PushRun-Project: $PROJECT"
			export GIT_CONFIG_KEY_4 GIT_CONFIG_VALUE_4
		fi
		export GIT_CONFIG_COUNT \
			GIT_CONFIG_KEY_0 GIT_CONFIG_VALUE_0 \
			GIT_CONFIG_KEY_1 GIT_CONFIG_VALUE_1 \
			GIT_CONFIG_KEY_2 GIT_CONFIG_VALUE_2 \
			GIT_CONFIG_KEY_3 GIT_CONFIG_VALUE_3
		git "$@"
	)
}

# --- commands --------------------------------------------------------------

# cmd_push_action implements run/sync/test/build: push HEAD to
# refs/pushrun/for/<branch> of the repo-identity URL, or route to rerun when
# the remote ref is already at HEAD.
cmd_push_action() {
	_action=$1
	require_repo
	require_config
	_id=$(repo_identity) || exit 1
	_p=$(resolve_project) || exit 1
	_b=$(current_branch)
	_u=$(user_meta)
	_head=$(git rev-parse HEAD) || die "no commits yet"
	_giturl="$SERVER/git/$_id.git"

	_remote=$(
		git_auth "$_u" "$_action" ls-remote "$_giturl" "refs/pushrun/for/$_b"
	) || die "cannot query $_giturl"
	_remote=$(printf '%s\n' "$_remote" | awk 'NF {print $1; exit}')

	if [ "$_remote" = "$_head" ]; then
		echo "pushrun: $_p is already at $_head — nothing to push; triggering rerun"
		_resp=$(api_request POST "/api/v1/instances/$_p/$INSTANCE/rerun" \
			"{\"branch\":\"$_b\",\"user\":\"$_u\"}") || exit 1
		_st=$(printf '%s' "$_resp" | json_field status)
		[ "$_st" = "SUCCESS" ] || die "rerun did not succeed (status: ${_st:-unknown})"
		echo "pushrun: rerun $_p/$INSTANCE: SUCCESS"
		return 0
	fi

	echo "pushrun: pushing $_b ($_head) to $_p/$INSTANCE on $SERVER"
	PUSH_LOG=$(mktemp "${TMPDIR:-/tmp}/pushrun-push.XXXXXX") || die "mktemp failed"
	# The push output (build log over the sideband) streams through tee while
	# a copy lands in PUSH_LOG for error inspection; git's exit code rides a
	# sidecar file because POSIX sh pipelines report only tee's status.
	{
		git_auth "$_u" "$_action" push --force "$_giturl" "HEAD:refs/pushrun/for/$_b" 2>&1
		printf '%s' "$?" >"$PUSH_LOG.rc"
	} | tee "$PUSH_LOG"
	_rc=$(cat "$PUSH_LOG.rc" 2>/dev/null || echo 1)
	case $_rc in '' | *[!0-9]*) _rc=1 ;; esac
	if [ "$_rc" -ne 0 ]; then
		if grep -Eq 'project_required:|ambiguous_project:' "$PUSH_LOG"; then
			_cands=$(sed -En 's/.*(project_required|ambiguous_project):([^ :]*).*/\2/p' "$PUSH_LOG" | head -n 1)
			echo "pushrun: re-push with --project <name> (candidates: ${_cands:-see above})" >&2
		fi
		die "git push to $_id failed"
	fi

	# The streamed CI_STATUS= trailer is display only — a build script can
	# print CI_STATUS=SUCCESS itself. The API (latest run record) is the
	# truth for the exit code.
	_runs=$(api_request GET "/api/v1/instances/$_p/$INSTANCE/runs") || exit 1
	_st=$(printf '%s' "$_runs" | json_field status)
	[ "$_st" = "SUCCESS" ] ||
		die "run did not succeed (status: ${_st:-unknown}) — see $SERVER"
}

# cmd_action implements start/stop/restart/rerun via the REST API (no push).
cmd_action() {
	_action=$1
	require_repo
	require_config
	_p=$(resolve_project) || exit 1
	_b=$(current_branch)
	_u=$(user_meta)
	_resp=$(api_request POST "/api/v1/instances/$_p/$INSTANCE/$_action" \
		"{\"branch\":\"$_b\",\"user\":\"$_u\"}") || exit 1
	_st=$(printf '%s' "$_resp" | json_field status)
	_url=$(printf '%s' "$_resp" | json_field url)
	echo "pushrun: $_action $_p/$INSTANCE: ${_st:-OK}${_url:+ — $_url}"
	[ "$_st" = "SUCCESS" ]
}

cmd_status() {
	require_repo
	require_config
	_p=$(resolve_project) || exit 1
	api_request GET "/api/v1/instances/$_p/$INSTANCE" || exit 1
	echo
}

# logs_files filters a logs/tree JSON body (stdin) into one path per line.
logs_files() {
	sed -e 's/^.*"files" *: *\[//' -e 's/\] *} *$//' |
		tr ',' '\n' |
		sed -e 's/^ *"//' -e 's/" *$//' -e '/^$/d'
}

# unescape_json reverses JSON string escaping, best effort.
unescape_json() {
	awk '{
		ph = sprintf("%c", 1)
		gsub(/\\\\/, ph)
		gsub(/\\n/, "\n")
		gsub(/\\t/, "\t")
		gsub(/\\r/, "\r")
		gsub(/\\"/, "\"")
		gsub(ph, "\\")
		printf "%s\n", $0
	}'
}

cmd_logs() {
	require_repo
	require_config
	_p=$(resolve_project) || exit 1
	_sub=${1:-ls}
	[ $# -gt 0 ] && shift
	case $_sub in
	ls | tree)
		# Capture the body before formatting: piping api_request directly
		# would mask a non-2xx exit code behind the formatter's.
		_resp=$(api_request GET "/api/v1/instances/$_p/$INSTANCE/logs/tree") || exit 1
		if [ "$_sub" = ls ]; then
			printf '%s\n' "$_resp" | logs_files
		else
			printf '%s\n' "$_resp" | logs_files | awk -F/ '{
				indent = ""
				for (i = 1; i < NF; i++) indent = indent "  "
				print indent $NF
			}'
		fi
		;;
	fetch)
		_path=${1:-}
		[ $# -gt 0 ] && shift
		[ -n "$_path" ] || die "usage: logs fetch <path> [-f]"
		_follow=0
		for _a in "$@"; do [ "$_a" = "-f" ] && _follow=1; done
		_enc=$(urlencode_path "$_path")
		if [ "$_follow" = 1 ]; then
			# Validate auth and path first: the streaming pipeline below
			# masks curl's exit code, so a 401/404 would silently exit 0.
			api_request GET "/api/v1/instances/$_p/$INSTANCE/logs/file?path=$_enc&tail_lines=1" >/dev/null || exit 1
			curl_auth_config
			curl -fsSN -K "$CURL_CONFIG" \
				"$SERVER/api/v1/instances/$_p/$INSTANCE/logs/file?path=$_enc&follow=1" |
				sed -n 's/^data: //p'
			return
		fi
		_resp=$(api_request GET "/api/v1/instances/$_p/$INSTANCE/logs/file?path=$_enc&tail_lines=200") || exit 1
		printf '%s' "$_resp" |
			sed -n 's/^.*"content" *: *"\(.*\)" *} *$/\1/p' | unescape_json
		;;
	*) die "unknown logs subcommand $_sub (want ls|tree|fetch)" ;;
	esac
}

cmd_doctor() {
	_rc=0
	echo "pushrun doctor"
	echo "client:  $PUSHRUN_CLIENT_VERSION"
	for _tool in git curl; do
		if command -v "$_tool" >/dev/null 2>&1; then
			printf '%-8s %s\n' "$_tool:" "$(command -v "$_tool")"
		else
			printf '%-8s %s\n' "$_tool:" MISSING
			_rc=1
		fi
	done
	if [ -f "$CONFIG_FILE" ]; then
		echo "config:  $CONFIG_FILE"
	else
		echo "config:  MISSING ($CONFIG_FILE) — run install.sh"
		_rc=1
	fi
	_alias=$(git config --global --get alias.pushrun 2>/dev/null || true)
	if [ -n "$_alias" ]; then
		echo "alias:   git pushrun -> $_alias"
	else
		echo "alias:   MISSING — run install.sh"
		_rc=1
	fi
	if [ -n "$SERVER" ] && [ -n "$TOKEN" ]; then
		curl_auth_config
		_v=$(curl -fsS --max-time 5 -K "$CURL_CONFIG" \
			"$SERVER/api/v1/version" 2>/dev/null | json_field version)
		if [ -n "$_v" ]; then
			echo "server:  $SERVER (version $_v)"
		else
			echo "server:  $SERVER UNREACHABLE or unauthorized"
			_rc=1
		fi
	else
		echo "server:  not configured"
		_rc=1
	fi
	if git rev-parse --git-dir >/dev/null 2>&1; then
		_id=$(repo_identity 2>/dev/null) || _id="(no remote.origin.url)"
		echo "repo:    identity $_id, branch $(current_branch)"
	else
		echo "repo:    not in a git repository"
	fi
	exit $_rc
}

cmd_update() {
	require_config
	curl_auth_config
	mkdir -p "$CACHE_DIR"
	curl -fsS -K "$CURL_CONFIG" "$SERVER/client.sh" -o "$SELF.tmp.$$" ||
		die "could not download $SERVER/client.sh"
	chmod +x "$SELF.tmp.$$"
	mv "$SELF.tmp.$$" "$SELF"
	_v=$(curl -fsS --max-time 5 -K "$CURL_CONFIG" \
		"$SERVER/api/v1/version" 2>/dev/null | json_field version)
	echo "pushrun: updated $SELF ($PUSHRUN_CLIENT_VERSION -> ${_v:-unknown})"
}

# --- main ------------------------------------------------------------------

pre_scan "$@"
load_config
SERVER=${SERVER:-$PUSHRUN_BAKED_SERVER}
SERVER=${SERVER%/}

_is_update=0
for _a in "$@"; do [ "$_a" = "update" ] && _is_update=1; done
[ "$_is_update" = 1 ] || handshake "$@"

# Extract global flags wherever they appear; rebuild "$@" with positionals.
_NL='
'
_POS=""
while [ $# -gt 0 ]; do
	case $1 in
	--instance) INSTANCE=${2:?--instance needs a value}; shift 2 ;;
	--instance=*) INSTANCE=${1#*=}; shift ;;
	--project) PROJECT=${2:?--project needs a value}; shift 2 ;;
	--project=*)
		PROJECT=${1#*=}
		[ -n "$PROJECT" ] || die "--project needs a value"
		shift
		;;
	--server | --token)
		[ $# -ge 2 ] || die "$1 needs a value"
		shift 2
		;; # already applied in pre_scan
	--server=* | --token=*) shift ;;
	-h | --help) usage; exit 0 ;;
	*)
		_POS=$_POS$_NL$1
		shift
		;;
	esac
done
if [ -n "$_POS" ]; then
	_POS=${_POS#?}
	_IFS=$IFS
	IFS=$_NL
	set -f
	# shellcheck disable=SC2086
	set -- $_POS
	set +f
	IFS=$_IFS
else
	set --
fi

case $INSTANCE in
"" | .* | *[/\\]*) die "invalid instance name \"$INSTANCE\"" ;;
esac
case $PROJECT in
"" | .* | *[/\\]*) [ -z "$PROJECT" ] || die "invalid project name \"$PROJECT\"" ;;
esac

_CMD=${1:-run}
[ $# -gt 0 ] && shift

case $_CMD in
run | sync | test | build) cmd_push_action "$_CMD" ;;
start | stop | restart | rerun) cmd_action "$_CMD" ;;
status) cmd_status ;;
logs) cmd_logs "$@" ;;
doctor) cmd_doctor ;;
update) cmd_update ;;
*) usage >&2; die "unknown command $_CMD" ;;
esac
