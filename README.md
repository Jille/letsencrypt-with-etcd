# letsencrypt-with-etcd

This is a letsencrypt client that uses etcd as its storage. It stores your (automatically created) LetsEncrypt account in `/letsencrypt-with-etcd/account` and (by default) stores your certificate in `/letsencrypt-with-etcd/yourdomain-fullchain.pem` and private key in `/letsencrypt-with-etcd/yourdomain-key.pem`.

It will refresh certificates if there's less than 1/3rd of the full expiry time remaining.

It tries to reuse your private key, but always writes the new certificate and (possibly new) private key in an atomic transaction to etcd.

You should forward requests to /.well-known/acme-challenge/ on your domains to this process.

# Parameters

## Environment

- `ETCD_ENDPOINTS` is where to find your etcd cluster
- `ETCD_USER` and `ETCD_PASSWORD` are used to connect to etcd. No authentication is used if you leave them unset/empty.

## Flags

- `--email` (`-e`) The email address for your LetsEncrypt account. (required)
- `--domains` (`-d`) Comma separated (or repeated) list of domain names to request together. The first one is used for the etcd key name. (required)
- `--port` (`-p`) Port to listen on. (default 8080)
- `--directory` Directory to write your certs/keys to etcd in. (default /letsencrypt-with-etcd/)
