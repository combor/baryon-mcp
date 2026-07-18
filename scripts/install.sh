#!/bin/sh

set -eu

REPOSITORY_URL="https://github.com/combor/baryon-mcp"
SUPPORTED_CLIENTS="claude codex"
KEYCHAIN_SERVICE="baryon-mcp"
KEYCHAIN_USERNAME_ACCOUNT="bridge-username"
KEYCHAIN_PASSWORD_ACCOUNT="bridge-password"

CLIENTS=""
CLIENTS_EXPLICIT=0
INSTALL_DIR=""
TLS_CERT_SOURCE=""
REQUESTED_VERSION=""
RESET_CREDENTIALS=0
SKIP_CLIENT_CONFIG=0
FORCE_CLIENT_CONFIG=0
TEMP_DIR=""
TERMINAL_ECHO_DISABLED=0
SECURITY_COMMAND=""

say() {
	printf '%s\n' "$*"
}

warn() {
	printf 'warning: %s\n' "$*" >&2
}

die() {
	printf 'error: %s\n' "$*" >&2
	exit 1
}

usage() {
	cat <<'EOF'
Install baryon-mcp and configure local stdio clients.

Usage: install.sh [options]

Options:
  --client NAME              Configure claude or codex (repeatable; default: both)
  --install-dir DIR          Binary directory (default: ~/bin on macOS,
                             ~/.local/bin on Linux)
  --tls-cert PATH            Proton Bridge exported cert.pem
  --version TAG              Install a specific release tag (default: latest)
  --reset-credentials        Replace stored Bridge credentials
  --force-client-config      Replace existing baryon client entries
  --skip-client-config       Install without configuring Claude or Codex
  -h, --help                 Show this help

On macOS, credentials are kept in Login Keychain. On Linux, they are kept in
separate mode-600 files below the user's configuration directory.
EOF
}

cleanup() {
	if [ -n "$TEMP_DIR" ] && [ -d "$TEMP_DIR" ]; then
		rm -rf "$TEMP_DIR"
	fi
}

restore_terminal_echo() {
	if [ "$TERMINAL_ECHO_DISABLED" -eq 1 ]; then
		stty echo 2>/dev/null || true
		TERMINAL_ECHO_DISABLED=0
	fi
}

cleanup_and_exit() {
	restore_terminal_echo
	exit 1
}

trap cleanup EXIT
trap cleanup_and_exit HUP INT TERM

append_client() {
	append_client_name=$1
	case " $SUPPORTED_CLIENTS " in
		*" $append_client_name "*) ;;
		*) die "unsupported client $append_client_name (supported: $SUPPORTED_CLIENTS)" ;;
	esac
	case " $CLIENTS " in
		*" $append_client_name "*) ;;
		*) CLIENTS="${CLIENTS:+$CLIENTS }$append_client_name" ;;
	esac
}

parse_args() {
	while [ "$#" -gt 0 ]; do
		case "$1" in
			--client)
				[ "$#" -ge 2 ] || die "--client requires a name"
				if [ "$CLIENTS_EXPLICIT" -eq 0 ]; then
					CLIENTS=""
					CLIENTS_EXPLICIT=1
				fi
				append_client "$2"
				shift 2
				;;
			--install-dir)
				[ "$#" -ge 2 ] || die "--install-dir requires a directory"
				INSTALL_DIR=$2
				shift 2
				;;
			--tls-cert)
				[ "$#" -ge 2 ] || die "--tls-cert requires a path"
				TLS_CERT_SOURCE=$2
				shift 2
				;;
			--version)
				[ "$#" -ge 2 ] || die "--version requires a tag"
				REQUESTED_VERSION=$2
				shift 2
				;;
			--reset-credentials)
				RESET_CREDENTIALS=1
				shift
				;;
			--force-client-config)
				FORCE_CLIENT_CONFIG=1
				shift
				;;
			--skip-client-config)
				SKIP_CLIENT_CONFIG=1
				shift
				;;
			-h | --help)
				usage
				exit 0
				;;
			*) die "unknown option: $1" ;;
		esac
	done

	if [ "$CLIENTS_EXPLICIT" -eq 0 ]; then
		for parse_client in $SUPPORTED_CLIENTS; do
			append_client "$parse_client"
		done
	fi
}

resolve_platform() {
	platform_os=${BARYON_INSTALLER_OS:-$(uname -s)}
	platform_arch=${BARYON_INSTALLER_ARCH:-$(uname -m)}

	case "$platform_os" in
		Darwin | darwin)
			OS=darwin
			ARCH=all
			ARCHIVE_FORMAT=tar.gz
			CONFIG_DIR="$HOME/.config/baryon-mcp"
			SECURITY_COMMAND=${BARYON_INSTALLER_SECURITY_COMMAND:-/usr/bin/security}
			[ -n "$INSTALL_DIR" ] || INSTALL_DIR="$HOME/bin"
			;;
		Linux | linux)
			OS=linux
			case "$platform_arch" in
				x86_64 | amd64) ARCH=amd64 ;;
				aarch64 | arm64) ARCH=arm64 ;;
				*) die "unsupported Linux architecture: $platform_arch" ;;
			esac
			ARCHIVE_FORMAT=tar.gz
			CONFIG_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/baryon-mcp"
			[ -n "$INSTALL_DIR" ] || INSTALL_DIR="$HOME/.local/bin"
			;;
		*) die "unsupported operating system: $platform_os (use install.ps1 on Windows)" ;;
	esac

	case "$INSTALL_DIR" in
		/*) ;;
		*) INSTALL_DIR="$(pwd -P)/$INSTALL_DIR" ;;
	esac
	case "$CONFIG_DIR" in
		/*) ;;
		*) CONFIG_DIR="$(pwd -P)/$CONFIG_DIR" ;;
	esac
}

resolve_release() {
	command -v curl >/dev/null 2>&1 || die "curl is required"

	if [ -n "$REQUESTED_VERSION" ]; then
		RELEASE_TAG=$REQUESTED_VERSION
		case "$RELEASE_TAG" in
			v*) ;;
			*) RELEASE_TAG="v$RELEASE_TAG" ;;
		esac
	else
		latest_url=$(curl -fsSL -o /dev/null -w '%{url_effective}' "$REPOSITORY_URL/releases/latest") \
			|| die "could not resolve the latest release"
		RELEASE_TAG=${latest_url##*/}
	fi

	RELEASE_VERSION=${RELEASE_TAG#v}
	case "$RELEASE_VERSION" in
		"" | *[!0-9A-Za-z._-]*) die "invalid release tag: $RELEASE_TAG" ;;
	esac

	RELEASE_BASE_URL=${BARYON_INSTALLER_RELEASE_BASE_URL:-"$REPOSITORY_URL/releases/download/$RELEASE_TAG"}
	RELEASE_BASE_URL=${RELEASE_BASE_URL%/}
	ARCHIVE_NAME="baryon-mcp_${RELEASE_VERSION}_${OS}_${ARCH}.${ARCHIVE_FORMAT}"
	CHECKSUM_NAME="baryon-mcp_${RELEASE_VERSION}_SHA256SUMS"
}

download_release() {
	TEMP_DIR=$(mktemp -d "${TMPDIR:-/tmp}/baryon-mcp.XXXXXX") \
		|| die "could not create a temporary directory"

	say "Downloading baryon-mcp $RELEASE_TAG for $OS/$ARCH..."
	curl -fsSL --retry 3 --retry-delay 1 -o "$TEMP_DIR/$ARCHIVE_NAME" \
		"$RELEASE_BASE_URL/$ARCHIVE_NAME" \
		|| die "could not download $ARCHIVE_NAME"
	curl -fsSL --retry 3 --retry-delay 1 -o "$TEMP_DIR/$CHECKSUM_NAME" \
		"$RELEASE_BASE_URL/$CHECKSUM_NAME" \
		|| die "could not download $CHECKSUM_NAME"
}

verify_release() {
	expected_hash=$(awk -v name="$ARCHIVE_NAME" '$2 == name { print $1 }' "$TEMP_DIR/$CHECKSUM_NAME")
	case "$expected_hash" in
		"" | *[!0-9A-Fa-f]*) die "no valid checksum found for $ARCHIVE_NAME" ;;
	esac
	[ "${#expected_hash}" -eq 64 ] || die "invalid checksum for $ARCHIVE_NAME"

	if command -v sha256sum >/dev/null 2>&1; then
		actual_hash=$(sha256sum "$TEMP_DIR/$ARCHIVE_NAME" | awk '{ print $1 }')
	elif command -v shasum >/dev/null 2>&1; then
		actual_hash=$(shasum -a 256 "$TEMP_DIR/$ARCHIVE_NAME" | awk '{ print $1 }')
	else
		die "sha256sum or shasum is required"
	fi

	expected_hash=$(printf '%s' "$expected_hash" | tr 'A-F' 'a-f')
	actual_hash=$(printf '%s' "$actual_hash" | tr 'A-F' 'a-f')
	[ "$actual_hash" = "$expected_hash" ] || die "checksum mismatch for $ARCHIVE_NAME"
}

install_binary() {
	extract_dir="$TEMP_DIR/extracted"
	mkdir -p "$extract_dir"
	tar -xzf "$TEMP_DIR/$ARCHIVE_NAME" -C "$extract_dir" baryon-mcp \
		|| die "could not extract $ARCHIVE_NAME"

	extracted_binary="$extract_dir/baryon-mcp"
	if [ ! -f "$extracted_binary" ] || [ -L "$extracted_binary" ]; then
		die "release archive does not contain a regular baryon-mcp binary"
	fi

	mkdir -p "$INSTALL_DIR"
	staged_binary="$INSTALL_DIR/.baryon-mcp.$$"
	cp "$extracted_binary" "$staged_binary"
	chmod 0755 "$staged_binary"
	mv -f "$staged_binary" "$INSTALL_DIR/baryon-mcp"
}

prompt_line() {
	prompt_text=$1
	printf '%s' "$prompt_text" >&2
	IFS= read -r PROMPT_VALUE
}

prompt_secret() {
	prompt_text=$1
	if [ -t 0 ]; then
		printf '%s' "$prompt_text" >&2
		TERMINAL_ECHO_DISABLED=1
		if ! stty -echo; then
			TERMINAL_ECHO_DISABLED=0
			return 1
		fi
		if ! IFS= read -r PROMPT_VALUE; then
			restore_terminal_echo
			return 1
		fi
		restore_terminal_echo
		printf '\n' >&2
	else
		printf '%s' "$prompt_text" >&2
		IFS= read -r PROMPT_VALUE || return 1
	fi
}

ensure_certificate() {
	mkdir -p "$CONFIG_DIR"
	chmod 0700 "$CONFIG_DIR"
	cert_target="$CONFIG_DIR/cert.pem"

	if [ -z "$TLS_CERT_SOURCE" ] && [ -n "${PROTON_BRIDGE_TLS_CERT:-}" ]; then
		TLS_CERT_SOURCE=$PROTON_BRIDGE_TLS_CERT
	fi
	if [ -z "$TLS_CERT_SOURCE" ] && [ -f "$cert_target" ]; then
		chmod 0600 "$cert_target"
		say "Reusing $cert_target"
		return
	fi

	if [ -z "$TLS_CERT_SOURCE" ]; then
		if [ "$OS" = darwin ]; then
			for cert_probe in \
				"$HOME/Library/Application Support/protonmail/bridge-v3/cert.pem" \
				"$HOME/Library/Application Support/protonmail/bridge/cert.pem"; do
				if [ -f "$cert_probe" ]; then
					TLS_CERT_SOURCE=$cert_probe
					break
				fi
			done
		else
			for cert_probe in \
				"$HOME/.config/protonmail/bridge-v3/cert.pem" \
				"$HOME/.config/protonmail/bridge/cert.pem"; do
				if [ -f "$cert_probe" ]; then
					TLS_CERT_SOURCE=$cert_probe
					break
				fi
			done
		fi
	fi

	if [ -z "$TLS_CERT_SOURCE" ]; then
		prompt_line "Path to Proton Bridge's exported cert.pem: " \
			|| die "a TLS certificate is required"
		TLS_CERT_SOURCE=$PROMPT_VALUE
	fi
	[ -f "$TLS_CERT_SOURCE" ] || die "TLS certificate not found: $TLS_CERT_SOURCE"
	grep -q -- '-----BEGIN CERTIFICATE-----' "$TLS_CERT_SOURCE" \
		|| die "TLS certificate does not contain a CERTIFICATE PEM block"

	if [ "$TLS_CERT_SOURCE" != "$cert_target" ]; then
		cert_stage="$CONFIG_DIR/.cert.pem.$$"
		cp "$TLS_CERT_SOURCE" "$cert_stage"
		chmod 0600 "$cert_stage"
		mv -f "$cert_stage" "$cert_target"
	fi
	chmod 0600 "$cert_target"
}

ensure_macos_credentials() {
	command -v "$SECURITY_COMMAND" >/dev/null 2>&1 || die "macOS security command not found"
	if [ "$RESET_CREDENTIALS" -eq 0 ] \
		&& "$SECURITY_COMMAND" find-generic-password -s "$KEYCHAIN_SERVICE" -a "$KEYCHAIN_USERNAME_ACCOUNT" >/dev/null 2>&1 \
		&& "$SECURITY_COMMAND" find-generic-password -s "$KEYCHAIN_SERVICE" -a "$KEYCHAIN_PASSWORD_ACCOUNT" >/dev/null 2>&1; then
		say "Reusing Baryon credentials from Login Keychain"
		return
	fi

	prompt_line "Proton Bridge IMAP username: " || die "Bridge username is required"
	bridge_username=$PROMPT_VALUE
	[ -n "$bridge_username" ] || die "Bridge username is required"

	say "Enter the Bridge-generated password at the Keychain prompt."
	"$SECURITY_COMMAND" add-generic-password -s "$KEYCHAIN_SERVICE" \
		-a "$KEYCHAIN_PASSWORD_ACCOUNT" -U -w \
		|| die "could not store the Bridge password in Keychain"
	if ! "$SECURITY_COMMAND" add-generic-password -s "$KEYCHAIN_SERVICE" \
		-a "$KEYCHAIN_USERNAME_ACCOUNT" -U -w "$bridge_username" >/dev/null; then
		"$SECURITY_COMMAND" delete-generic-password -s "$KEYCHAIN_SERVICE" \
			-a "$KEYCHAIN_PASSWORD_ACCOUNT" >/dev/null 2>&1 || true
		die "could not store the Bridge username in Keychain"
	fi
}

ensure_linux_credentials() {
	username_file="$CONFIG_DIR/bridge-username"
	password_file="$CONFIG_DIR/bridge-password"
	if [ "$RESET_CREDENTIALS" -eq 0 ] && [ -s "$username_file" ] && [ -s "$password_file" ]; then
		chmod 0600 "$username_file" "$password_file"
		say "Reusing Proton Bridge credentials from $CONFIG_DIR"
		return
	fi

	prompt_line "Proton Bridge IMAP username: " || die "Bridge username is required"
	bridge_username=$PROMPT_VALUE
	prompt_secret "Proton Bridge-generated password: " || die "Bridge password is required"
	bridge_password=$PROMPT_VALUE
	[ -n "$bridge_username" ] || die "Bridge username is required"
	[ -n "$bridge_password" ] || die "Bridge password is required"

	umask 077
	printf '%s' "$bridge_username" >"$username_file"
	printf '%s' "$bridge_password" >"$password_file"
	chmod 0600 "$username_file" "$password_file"
	bridge_password=""
}

ensure_credentials() {
	if [ "$OS" = darwin ]; then
		ensure_macos_credentials
	else
		ensure_linux_credentials
	fi
}

quote_sh() {
	printf "'"
	printf '%s' "$1" | sed "s/'/'\\\\''/g"
	printf "'"
}

# The generated launcher intentionally contains command substitutions that must
# run later, not while the installer writes the file.
# shellcheck disable=SC2016
write_launcher() {
	launcher_path="$INSTALL_DIR/baryon-launch"
	launcher_stage="$INSTALL_DIR/.baryon-launch.$$"
	quoted_config_dir=$(quote_sh "$CONFIG_DIR")

	{
		printf '%s\n' '#!/bin/sh' 'set -eu'
		printf 'config_dir=%s\n' "$quoted_config_dir"
		if [ "$OS" = darwin ]; then
			quoted_service=$(quote_sh "$KEYCHAIN_SERVICE")
			quoted_username_account=$(quote_sh "$KEYCHAIN_USERNAME_ACCOUNT")
			quoted_password_account=$(quote_sh "$KEYCHAIN_PASSWORD_ACCOUNT")
			printf 'export PROTON_BRIDGE_USERNAME="$(/usr/bin/security find-generic-password -s %s -a %s -w)"\n' \
				"$quoted_service" "$quoted_username_account"
			printf 'export PROTON_BRIDGE_PASSWORD="$(/usr/bin/security find-generic-password -s %s -a %s -w)"\n' \
				"$quoted_service" "$quoted_password_account"
		else
			printf '%s\n' \
				'export PROTON_BRIDGE_USERNAME="$(cat "$config_dir/bridge-username")"' \
				'export PROTON_BRIDGE_PASSWORD="$(cat "$config_dir/bridge-password")"'
		fi
		printf '%s\n' \
			'export PROTON_BRIDGE_TLS_CERT="$config_dir/cert.pem"' \
			'script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)' \
			'exec "$script_dir/baryon-mcp"'
	} >"$launcher_stage"
	chmod 0755 "$launcher_stage"
	mv -f "$launcher_stage" "$launcher_path"
	LAUNCHER_PATH=$launcher_path
}

configure_claude() {
	if ! command -v claude >/dev/null 2>&1; then
		warn "Claude Code is not installed; skipped Claude configuration"
		return
	fi
	if run_client_cli claude mcp get baryon >/dev/null 2>&1; then
		if [ "$FORCE_CLIENT_CONFIG" -eq 0 ]; then
			warn "Claude already has a baryon entry; left it unchanged"
			return
		fi
		run_client_cli claude mcp remove --scope user baryon >/dev/null 2>&1 || true
	fi
	run_client_cli claude mcp add --transport stdio --scope user baryon -- "$LAUNCHER_PATH" \
		|| die "could not configure Claude Code"
	say "Configured Claude Code (user scope)"
}

configure_codex() {
	if ! command -v codex >/dev/null 2>&1; then
		warn "Codex is not installed; skipped Codex configuration"
		return
	fi
	if run_client_cli codex mcp get baryon --json >/dev/null 2>&1; then
		if [ "$FORCE_CLIENT_CONFIG" -eq 0 ]; then
			warn "Codex already has a baryon entry; left it unchanged"
			return
		fi
		run_client_cli codex mcp remove baryon >/dev/null 2>&1 || true
	fi
	run_client_cli codex mcp add baryon -- "$LAUNCHER_PATH" \
		|| die "could not configure Codex"
	say "Configured Codex"
}

run_client_cli() {
	# Avoid mistaking a project-local entry in the caller's directory for the
	# user-level entry managed by this installer.
	(cd "$TEMP_DIR" && "$@")
}

configure_client() {
	# Add a client name to SUPPORTED_CLIENTS and one adapter here.
	case "$1" in
		claude) configure_claude ;;
		codex) configure_codex ;;
		*) die "no adapter implemented for client $1" ;;
	esac
}

configure_clients() {
	[ "$SKIP_CLIENT_CONFIG" -eq 0 ] || return 0
	for configure_client_name in $CLIENTS; do
		configure_client "$configure_client_name"
	done
}

main() {
	parse_args "$@"
	[ -n "${HOME:-}" ] || die "HOME is not set"
	resolve_platform
	resolve_release
	download_release
	verify_release
	install_binary
	ensure_certificate
	ensure_credentials
	write_launcher
	configure_clients

	say "Installed baryon-mcp $RELEASE_TAG"
	say "  binary:   $INSTALL_DIR/baryon-mcp"
	say "  launcher: $LAUNCHER_PATH"
}

main "$@"
