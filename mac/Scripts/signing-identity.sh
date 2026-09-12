#!/bin/bash
# Creates a stable, local code-signing identity for Porthmoss.
#
# Why this exists: macOS grants Accessibility and Input Monitoring to a code
# identity. An ad-hoc signature has no stable identity — its hash changes on
# every build — so each rebuild silently revoked both permissions while System
# Settings still showed the switches as on. That is unusable for an app you are
# actively developing, and it produced a working mouse with a dead keyboard,
# because the two permissions fail differently.
#
# A self-signed certificate gives the app a designated requirement that stays
# put, so a grant made once survives every later build.
#
# It lives in its own keychain with a known password, so nothing prompts and
# nothing touches the login keychain. To remove it entirely:
#   security delete-keychain ~/Library/Keychains/porthmoss-signing.keychain-db
set -euo pipefail

IDENTITY="Porthmoss Local Signing"
KEYCHAIN="$HOME/Library/Keychains/porthmoss-signing.keychain-db"
KEYCHAIN_PASSWORD="porthmoss"

if security find-certificate -c "$IDENTITY" "$KEYCHAIN" >/dev/null 2>&1; then
    echo "$IDENTITY"
    exit 0
fi

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

# codeSigning EKU is what makes codesign accept it; CA:false keeps it a leaf.
cat > "$WORK/openssl.cnf" <<'CONF'
[req]
distinguished_name = dn
x509_extensions = ext
prompt = no
[dn]
CN = Porthmoss Local Signing
[ext]
basicConstraints = critical,CA:false
keyUsage = critical,digitalSignature
extendedKeyUsage = critical,codeSigning
CONF

openssl req -x509 -newkey rsa:2048 -nodes -days 3650 \
    -keyout "$WORK/key.pem" -out "$WORK/cert.pem" -config "$WORK/openssl.cnf" 2>/dev/null
# OpenSSL 3 defaults to AES-256 with a SHA-256 MAC, which Apple's `security
# import` cannot read ("MAC verification failed"). These are the algorithms it
# does understand.
openssl pkcs12 -export -out "$WORK/identity.p12" \
    -inkey "$WORK/key.pem" -in "$WORK/cert.pem" \
    -name "$IDENTITY" -passout pass:"$KEYCHAIN_PASSWORD" \
    -certpbe PBE-SHA1-3DES -keypbe PBE-SHA1-3DES -macalg sha1 2>/dev/null

security create-keychain -p "$KEYCHAIN_PASSWORD" "$KEYCHAIN" 2>/dev/null || true
security unlock-keychain -p "$KEYCHAIN_PASSWORD" "$KEYCHAIN"
# No auto-lock, or signing starts prompting again after a while.
security set-keychain-settings "$KEYCHAIN"
security import "$WORK/identity.p12" -k "$KEYCHAIN" -P "$KEYCHAIN_PASSWORD" \
    -T /usr/bin/codesign -T /usr/bin/security >/dev/null
# Lets codesign use the key without a prompt.
security set-key-partition-list -S apple-tool:,apple: -s \
    -k "$KEYCHAIN_PASSWORD" "$KEYCHAIN" >/dev/null 2>&1

# codesign only searches keychains in the user's list.
EXISTING="$(security list-keychains -d user | sed 's/[" ]//g' | tr '\n' ' ')"
case "$EXISTING" in
    *porthmoss-signing*) ;;
    *) security list-keychains -d user -s $EXISTING "$KEYCHAIN" ;;
esac

echo "$IDENTITY"
