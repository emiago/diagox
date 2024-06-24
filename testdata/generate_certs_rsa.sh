#!/usr/bin/env bash

mkdir -p certs && cd certs

CN="localhost"
subjectAltName="IP.1:127.0.0.1,IP.2:172.18.0.2,IP.2:172.18.0.3,IP.2:172.18.0.4,DNS:localhost"

set -x
# Create root CA
openssl genrsa -out rootca-key.pem 2048

openssl req -new -x509 -nodes -days 3650 \
    -subj "/C=US/ST=California/CN=$CN" \
   -key rootca-key.pem \
   -out rootca-cert.pem


# Server
openssl req -newkey rsa:2048 -nodes \
    -subj "/C=US/ST=California/CN=$CN" \
   -keyout server.key \
   -out server.csr


openssl x509 -req -days 3650 -set_serial 01 \
    -in server.csr \
    -out server.crt \
    -CA rootca-cert.pem \
    -CAkey rootca-key.pem \
    -extensions SAN   \
    -extfile <(printf "\n[SAN]\nsubjectAltName=$subjectAltName\nextendedKeyUsage=serverAuth")


echo "Server cert and key created"
echo "==========================="
openssl x509 -noout -text -in server.crt
echo "==========================="

# Client
openssl req -newkey rsa:2048 -nodes \
    -subj "/C=US/ST=California/CN=$CN" \
   -keyout client.key \
   -out client.csr


openssl x509 -req -days 3650 -set_serial 01  \
    -in client.csr  \
    -out client.crt  \
    -CA rootca-cert.pem \
    -CAkey rootca-key.pem \
    -extensions SAN  \
    -extfile <(printf "\n[SAN]\nsubjectAltName=$subjectAltName\nextendedKeyUsage=clientAuth")


echo "Client cert and key created"
echo "==========================="
openssl x509 -noout -text -in client.crt
echo "==========================="
set +x