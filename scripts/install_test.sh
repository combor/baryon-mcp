#!/usr/bin/env bash

set -euo pipefail

REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
TEST_DIR=$(mktemp -d "${TMPDIR:-/tmp}/baryon-installer-test.XXXXXX")
trap 'rm -rf "$TEST_DIR"' EXIT

fail() {
	printf 'FAIL: %s\n' "$*" >&2
	exit 1
}

sha256_file() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | awk '{ print $1 }'
	else
		shasum -a 256 "$1" | awk '{ print $1 }'
	fi
}

file_mode() {
	if stat -c '%a' "$1" >/dev/null 2>&1; then
		stat -c '%a' "$1"
	else
		stat -f '%Lp' "$1"
	fi
}

VERSION=9.9.9
RELEASE_DIR="$TEST_DIR/release"
PAYLOAD_DIR="$TEST_DIR/payload"
MOCK_BIN="$TEST_DIR/mock-bin"
HOME_DIR="$TEST_DIR/home"
INSTALL_DIR="$HOME_DIR/.local/bin"
CONFIG_HOME="$HOME_DIR/config"
CLIENT_LOG="$TEST_DIR/clients.log"
SECURITY_LOG="$TEST_DIR/security.log"
CERT_PATH="$TEST_DIR/exported-cert.pem"
ARCHIVE="baryon-mcp_${VERSION}_linux_amd64.tar.gz"
MAC_ARCHIVE="baryon-mcp_${VERSION}_darwin_all.tar.gz"
CHECKSUMS="baryon-mcp_${VERSION}_SHA256SUMS"

mkdir -p "$RELEASE_DIR" "$PAYLOAD_DIR" "$MOCK_BIN" "$HOME_DIR"

cat >"$PAYLOAD_DIR/baryon-mcp" <<'EOF'
#!/bin/sh
printf '%s|%s|%s\n' "$PROTON_BRIDGE_USERNAME" "$PROTON_BRIDGE_PASSWORD" "$PROTON_BRIDGE_TLS_CERT"
EOF
chmod 0755 "$PAYLOAD_DIR/baryon-mcp"
tar -czf "$RELEASE_DIR/$ARCHIVE" -C "$PAYLOAD_DIR" baryon-mcp
printf '%s  %s\n' "$(sha256_file "$RELEASE_DIR/$ARCHIVE")" "$ARCHIVE" >"$RELEASE_DIR/$CHECKSUMS"
tar -czf "$RELEASE_DIR/$MAC_ARCHIVE" -C "$PAYLOAD_DIR" baryon-mcp
printf '%s  %s\n' "$(sha256_file "$RELEASE_DIR/$MAC_ARCHIVE")" "$MAC_ARCHIVE" >>"$RELEASE_DIR/$CHECKSUMS"

cat >"$CERT_PATH" <<'EOF'
-----BEGIN CERTIFICATE-----
installer-test
-----END CERTIFICATE-----
EOF

for client in claude codex; do
	cat >"$MOCK_BIN/$client" <<'EOF'
#!/bin/sh
printf '%s:%s\n' "$(basename "$0")" "$*" >>"$BARYON_TEST_CLIENT_LOG"
if [ "${BARYON_TEST_FORBIDDEN_CLIENT_CWD:-}" = "$PWD" ]; then
	exit 91
fi
if [ "${BARYON_TEST_EXISTING:-0}" = 1 ] && [ "${2:-}" = get ]; then
	exit 0
fi
if [ "${2:-}" = get ]; then
	exit 1
fi
exit 0
EOF
	chmod 0755 "$MOCK_BIN/$client"
done

cat >"$TEST_DIR/security" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >>"$BARYON_TEST_SECURITY_LOG"
case "$1" in
	find-generic-password)
		[ "${BARYON_TEST_SECURITY_EXISTING:-0}" = 1 ] && exit 0
		exit 1
		;;
	add-generic-password)
		previous=""
		for argument do
			if [ "$previous" = -a ] && [ "$argument" = "${BARYON_TEST_SECURITY_FAIL_ACCOUNT:-}" ]; then
				exit 1
			fi
			previous=$argument
		done
		exit 0
		;;
	delete-generic-password) exit 0 ;;
	*) exit 2 ;;
esac
EOF
chmod 0755 "$TEST_DIR/security"

run_installer() {
	env \
		HOME="$HOME_DIR" \
		XDG_CONFIG_HOME="$CONFIG_HOME" \
		PATH="$MOCK_BIN:$PATH" \
		BARYON_INSTALLER_OS=linux \
		BARYON_INSTALLER_ARCH=amd64 \
		BARYON_INSTALLER_RELEASE_BASE_URL="file://$RELEASE_DIR" \
		BARYON_TEST_CLIENT_LOG="$CLIENT_LOG" \
		BARYON_TEST_FORBIDDEN_CLIENT_CWD="$REPO_ROOT" \
		"$@"
}

run_macos_installer() {
	mac_home_dir=$1
	mac_security_log=$2
	shift 2
	mkdir -p "$mac_home_dir"
	printf 'mac-bridge-user\n' | env \
		HOME="$mac_home_dir" \
		BARYON_INSTALLER_OS=darwin \
		BARYON_INSTALLER_ARCH=arm64 \
		BARYON_INSTALLER_RELEASE_BASE_URL="file://$RELEASE_DIR" \
		BARYON_INSTALLER_SECURITY_COMMAND="$TEST_DIR/security" \
		BARYON_TEST_SECURITY_LOG="$mac_security_log" \
		"$@" \
		sh "$REPO_ROOT/scripts/install.sh" \
		--version "v$VERSION" \
		--tls-cert "$CERT_PATH" \
		--skip-client-config
}

printf 'bridge-user\ns3cr3t-value\n' | run_installer \
	sh "$REPO_ROOT/scripts/install.sh" \
	--version "v$VERSION" \
	--tls-cert "$CERT_PATH" >/dev/null

[ -x "$INSTALL_DIR/baryon-mcp" ] || fail "binary was not installed"
[ -x "$INSTALL_DIR/baryon-launch" ] || fail "launcher was not installed"
[ "$(file_mode "$CONFIG_HOME/baryon-mcp")" = 700 ] || fail "config directory mode is not 700"
[ "$(file_mode "$CONFIG_HOME/baryon-mcp/bridge-username")" = 600 ] || fail "username mode is not 600"
[ "$(file_mode "$CONFIG_HOME/baryon-mcp/bridge-password")" = 600 ] || fail "password mode is not 600"
[ "$(file_mode "$CONFIG_HOME/baryon-mcp/cert.pem")" = 600 ] || fail "certificate mode is not 600"

expected_launch="bridge-user|s3cr3t-value|$CONFIG_HOME/baryon-mcp/cert.pem"
[ "$(HOME="$HOME_DIR" XDG_CONFIG_HOME="$CONFIG_HOME" "$INSTALL_DIR/baryon-launch")" = "$expected_launch" ] \
	|| fail "launcher did not pass credentials and certificate to baryon-mcp"
if grep -Fq 's3cr3t-value' "$INSTALL_DIR/baryon-launch"; then
	fail "launcher contains the Bridge password"
fi
grep -Fq "claude:mcp add --transport stdio --scope user baryon -- $INSTALL_DIR/baryon-launch" "$CLIENT_LOG" \
	|| fail "Claude adapter used unexpected arguments"
grep -Fq "codex:mcp add baryon -- $INSTALL_DIR/baryon-launch" "$CLIENT_LOG" \
	|| fail "Codex adapter used unexpected arguments"

chmod 0644 \
	"$CONFIG_HOME/baryon-mcp/cert.pem" \
	"$CONFIG_HOME/baryon-mcp/bridge-username" \
	"$CONFIG_HOME/baryon-mcp/bridge-password"
run_installer \
	BARYON_TEST_EXISTING=1 \
	sh "$REPO_ROOT/scripts/install.sh" \
	--version "v$VERSION" \
	--install-dir "$INSTALL_DIR" \
	--client claude \
	--client codex >/dev/null 2>&1
[ "$(grep -Fc 'claude:mcp add ' "$CLIENT_LOG")" = 1 ] || fail "existing Claude entry was overwritten"
[ "$(grep -Fc 'codex:mcp add ' "$CLIENT_LOG")" = 1 ] || fail "existing Codex entry was overwritten"
[ "$(file_mode "$CONFIG_HOME/baryon-mcp/cert.pem")" = 600 ] || fail "reused certificate mode was not repaired"
[ "$(file_mode "$CONFIG_HOME/baryon-mcp/bridge-username")" = 600 ] || fail "reused username mode was not repaired"
[ "$(file_mode "$CONFIG_HOME/baryon-mcp/bridge-password")" = 600 ] || fail "reused password mode was not repaired"

run_installer \
	BARYON_TEST_EXISTING=1 \
	sh "$REPO_ROOT/scripts/install.sh" \
	--version "v$VERSION" \
	--install-dir "$INSTALL_DIR" \
	--client claude \
	--client codex \
	--force-client-config >/dev/null
[ "$(grep -Fc 'claude:mcp add ' "$CLIENT_LOG")" = 2 ] || fail "Claude entry was not replaced when forced"
[ "$(grep -Fc 'codex:mcp add ' "$CLIENT_LOG")" = 2 ] || fail "Codex entry was not replaced when forced"

MAC_HOME="$TEST_DIR/mac-home"
run_macos_installer "$MAC_HOME" "$SECURITY_LOG" >/dev/null
[ -x "$MAC_HOME/bin/baryon-launch" ] || fail "macOS launcher was not installed in ~/bin"
grep -Fq '/usr/bin/security find-generic-password' "$MAC_HOME/bin/baryon-launch" \
	|| fail "macOS launcher does not read credentials from Login Keychain"
grep -Fq -- "-s 'baryon-mcp' -a 'bridge-username'" "$MAC_HOME/bin/baryon-launch" \
	|| fail "macOS launcher does not use the Baryon username Keychain entry"
grep -Fq -- "-s 'baryon-mcp' -a 'bridge-password'" "$MAC_HOME/bin/baryon-launch" \
	|| fail "macOS launcher does not use the Baryon password Keychain entry"
if grep -Fq 'mac-bridge-user' "$MAC_HOME/bin/baryon-launch"; then
	fail "macOS launcher contains the Bridge username"
fi
password_store_line=$(grep -n 'add-generic-password.*-s baryon-mcp.*-a bridge-password' "$SECURITY_LOG" | cut -d: -f1)
username_store_line=$(grep -n 'add-generic-password.*-s baryon-mcp.*-a bridge-username' "$SECURITY_LOG" | cut -d: -f1)
[ "$password_store_line" -lt "$username_store_line" ] \
	|| fail "macOS credentials were stored before the password prompt succeeded"
grep -Eq 'add-generic-password .*baryon-mcp.*bridge-password -U -w$' "$SECURITY_LOG" \
	|| fail "macOS password was passed as a process argument"
if grep -Eq 'proton-bridge-(username|password)' "$REPO_ROOT/scripts/install.sh"; then
	fail "installer depends on legacy local Keychain item names"
fi

REUSE_SECURITY_LOG="$TEST_DIR/security-reuse.log"
run_macos_installer "$MAC_HOME" "$REUSE_SECURITY_LOG" \
	BARYON_TEST_SECURITY_EXISTING=1 >/dev/null
grep -q 'find-generic-password -s baryon-mcp -a bridge-username' "$REUSE_SECURITY_LOG" \
	|| fail "macOS installer did not look up the Baryon username entry"
grep -q 'find-generic-password -s baryon-mcp -a bridge-password' "$REUSE_SECURITY_LOG" \
	|| fail "macOS installer did not look up the Baryon password entry"
if grep -q 'add-generic-password' "$REUSE_SECURITY_LOG"; then
	fail "existing Baryon Keychain credentials were replaced"
fi

PASSWORD_FAILURE_LOG="$TEST_DIR/security-password-failure.log"
if run_macos_installer "$TEST_DIR/mac-password-failure" "$PASSWORD_FAILURE_LOG" \
	BARYON_TEST_SECURITY_FAIL_ACCOUNT=bridge-password >"$TEST_DIR/mac-password-failure.out" 2>&1; then
	fail "macOS password storage failure was accepted"
fi
if grep -q 'add-generic-password.*-a bridge-username' "$PASSWORD_FAILURE_LOG"; then
	fail "macOS username was updated after password storage failed"
fi

USERNAME_FAILURE_LOG="$TEST_DIR/security-username-failure.log"
if run_macos_installer "$TEST_DIR/mac-username-failure" "$USERNAME_FAILURE_LOG" \
	BARYON_TEST_SECURITY_FAIL_ACCOUNT=bridge-username >"$TEST_DIR/mac-username-failure.out" 2>&1; then
	fail "macOS username storage failure was accepted"
fi
grep -q 'delete-generic-password.*-s baryon-mcp.*-a bridge-password' "$USERNAME_FAILURE_LOG" \
	|| fail "macOS password was not rolled back after username storage failed"

RELATIVE_ROOT="$TEST_DIR/relative-paths"
mkdir -p "$RELATIVE_ROOT/home"
RELATIVE_ROOT_PHYSICAL=$(cd "$RELATIVE_ROOT" && pwd -P)
(
	cd "$RELATIVE_ROOT"
	printf 'relative-user\nrelative-password\n' | env \
		HOME="$RELATIVE_ROOT/home" \
		XDG_CONFIG_HOME=config \
		BARYON_INSTALLER_OS=linux \
		BARYON_INSTALLER_ARCH=amd64 \
		BARYON_INSTALLER_RELEASE_BASE_URL="file://$RELEASE_DIR" \
		sh "$REPO_ROOT/scripts/install.sh" \
		--version "v$VERSION" \
		--install-dir bin \
		--tls-cert "$CERT_PATH" \
		--skip-client-config >/dev/null
)
[ -x "$RELATIVE_ROOT_PHYSICAL/bin/baryon-launch" ] || fail "relative install directory was not normalized"
grep -Fq "config_dir='$RELATIVE_ROOT_PHYSICAL/config/baryon-mcp'" "$RELATIVE_ROOT_PHYSICAL/bin/baryon-launch" \
	|| fail "relative config directory was not normalized"

BAD_RELEASE_DIR="$TEST_DIR/bad-release"
BAD_HOME="$TEST_DIR/bad-home"
mkdir -p "$BAD_RELEASE_DIR" "$BAD_HOME"
cp "$RELEASE_DIR/$ARCHIVE" "$BAD_RELEASE_DIR/$ARCHIVE"
printf '%064d  %s\n' 0 "$ARCHIVE" >"$BAD_RELEASE_DIR/$CHECKSUMS"
if printf 'unused-user\nunused-password\n' | env \
	HOME="$BAD_HOME" \
	XDG_CONFIG_HOME="$BAD_HOME/config" \
	BARYON_INSTALLER_OS=linux \
	BARYON_INSTALLER_ARCH=amd64 \
	BARYON_INSTALLER_RELEASE_BASE_URL="file://$BAD_RELEASE_DIR" \
	sh "$REPO_ROOT/scripts/install.sh" \
	--version "v$VERSION" \
	--install-dir "$BAD_HOME/bin" \
	--tls-cert "$CERT_PATH" \
	--skip-client-config >"$TEST_DIR/bad.out" 2>&1; then
	fail "checksum mismatch was accepted"
fi
grep -Fq 'checksum mismatch' "$TEST_DIR/bad.out" || fail "checksum failure was not explained"
[ ! -e "$BAD_HOME/bin/baryon-mcp" ] || fail "bad archive installed a binary"

printf 'installer tests passed\n'
