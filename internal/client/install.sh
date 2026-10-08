#!/bin/sh
# pushrun installer (rendered server-side; version {{.Version}}).
#
# Bootstraps the pushrun client on this machine:
#   1. stores the server URL + token in ~/.config/pushrun/config (0600)
#   2. caches client.sh at ~/.local/share/pushrun/client.sh
#   3. installs the git alias: git pushrun -> the cached client.sh
#
# Usage: install.sh [--server URL] [--token TOKEN]
# With no --token, the token is prompted for on the terminal. There is no
# verification handshake beyond downloading client.sh itself: a wrong token
# fails here with a 401, and nothing is written.

set -eu

PUSHRUN_VERSION="{{.Version}}"
SERVER="{{.ServerURL}}"
TOKEN=""

while [ $# -gt 0 ]; do
	case $1 in
	--server) SERVER=${2:?--server needs a value}; shift 2 ;;
	--server=*) SERVER=${1#*=}; shift ;;
	--token) TOKEN=${2:?--token needs a value}; shift 2 ;;
	--token=*) TOKEN=${1#*=}; shift ;;
	-h | --help)
		echo "usage: install.sh [--server URL] [--token TOKEN]"
		exit 0
		;;
	*) echo "install.sh: unknown argument $1" >&2; exit 2 ;;
	esac
done

SERVER=${SERVER%/}

for tool in git curl; do
	command -v "$tool" >/dev/null 2>&1 || {
		echo "install.sh: $tool is required but not installed" >&2
		exit 1
	}
done

if [ -z "$TOKEN" ]; then
	if [ ! -r /dev/tty ]; then
		echo "install.sh: no --token given and no terminal to prompt on" >&2
		exit 1
	fi
	printf 'pushrun token for %s: ' "$SERVER" >/dev/tty
	read -r TOKEN </dev/tty
fi
[ -n "$TOKEN" ] || {
	echo "install.sh: empty token" >&2
	exit 1
}

CONFIG_DIR="$HOME/.config/pushrun"
CONFIG_FILE="$CONFIG_DIR/config"
CACHE_DIR="$HOME/.local/share/pushrun"
CLIENT="$CACHE_DIR/client.sh"

# Downloading client.sh doubles as the token check: a wrong token 401s here
# and nothing below is written. The token rides in a 0600 curl config file
# instead of argv, where `ps` would show it to other local users.
CURL_CONFIG=$(mktemp "${TMPDIR:-/tmp}/pushrun-install.XXXXXX")
trap 'rm -f "$CURL_CONFIG"' EXIT
trap 'exit 1' HUP INT TERM
chmod 600 "$CURL_CONFIG"
printf 'header = "Authorization: Bearer %s"\n' "$TOKEN" >"$CURL_CONFIG"

mkdir -p "$CACHE_DIR"
if ! curl -fsSL -K "$CURL_CONFIG" "$SERVER/client.sh" -o "$CLIENT"; then
	echo "install.sh: could not download $SERVER/client.sh" >&2
	echo "install.sh: if the server answered 401 unauthorized, the token is wrong" >&2
	exit 1
fi
chmod +x "$CLIENT"

umask 077
mkdir -p "$CONFIG_DIR"
{
	echo "server=$SERVER"
	echo "token=$TOKEN"
} >"$CONFIG_FILE"
chmod 600 "$CONFIG_FILE"

ALIAS='!~/.local/share/pushrun/client.sh'
git config --global alias.pushrun "$ALIAS"

echo "pushrun $PUSHRUN_VERSION installed."
echo "  wrote   $CONFIG_FILE (mode 0600)"
echo "  cached  $CLIENT"
echo "  alias   alias.pushrun = $ALIAS (git pushrun ...)"
echo
echo "Try it: cd into a project repo and run 'git pushrun'."
