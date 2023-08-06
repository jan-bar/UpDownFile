#!/bin/bash

expire=3650 # 10 year
keybit=2048
domain=janbar.com

# generate ca.key,ca.csr,ca.crt
openssl genrsa -out ca.key $keybit
openssl req -new -subj "/C=CN/ST=ShangHai/L=SH/O=Janbar/OU=IT/CN=ca.$domain" -key ca.key -out ca.csr
openssl x509 -req -days $expire -sha512 -extensions v3_ca -signkey ca.key -in ca.csr -out ca.crt

# 生成janbar.key,janbar.csr,janbar.cert
openssl genrsa -out janbar.key $keybit
openssl req -sha512 -new -subj "/C=CN/ST=ShangHai/L=SH/O=Janbar/OU=IT/CN=$domain" -key janbar.key -out janbar.csr
cat > v3.ext <<-EOF
[v3_req]
authorityKeyIdentifier = keyid,issuer
basicConstraints = CA:FALSE
keyUsage = digitalSignature, nonRepudiation, keyEncipherment, dataEncipherment
extendedKeyUsage = serverAuth
subjectAltName = @alt_names
[alt_names]
DNS.1=$domain
IP.1=127.0.0.1
EOF
openssl x509 -req -sha512 -days $expire -extensions v3_req -extfile v3.ext -CA ca.crt -CAkey ca.key -CAcreateserial -in janbar.csr -out janbar.cert

# three files are needed
# sz ca.crt janbar.key janbar.cert

# curl --cacert ca.crt -s https://$domain
# wget --ca-certificate ca.crt -qO- https://$domain
