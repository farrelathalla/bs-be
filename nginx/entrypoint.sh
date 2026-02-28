#!/bin/sh
# Install openssl if not present
apk add --no-cache openssl 2>/dev/null || true

# Generate self-signed certificate if not exists
if [ ! -f /etc/nginx/certs/selfsigned.crt ]; then
    mkdir -p /etc/nginx/certs
    openssl req -x509 -nodes -days 3650 -newkey rsa:2048 \
        -keyout /etc/nginx/certs/selfsigned.key \
        -out /etc/nginx/certs/selfsigned.crt \
        -subj "/C=ID/ST=Jakarta/L=Jakarta/O=BetterBankings/CN=103.103.22.207"
fi

exec nginx -g 'daemon off;'
