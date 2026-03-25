# GitHub Pages RPC Proxy

This guide pairs [github-pages.md](/Users/efrainvera/Documents/UNIFIED/docs/github-pages.md) with a real HTTPS reverse proxy in front of a UniFied node.

Use this when:

- the public site is hosted on GitHub Pages
- the node runs on a VPS or dedicated server
- browser clients need `https://.../rpc` and `wss://.../ws`

## Files

- nginx template: [unified-rpc-proxy.conf.tmpl](/Users/efrainvera/Documents/UNIFIED/deploy/nginx/unified-rpc-proxy.conf.tmpl)
- Pages runtime config: [site-config.js](/Users/efrainvera/Documents/UNIFIED/web/site/assets/site-config.js)

## 1. Expose the node locally

Keep the node local to the server and let nginx face the internet:

```bash
sudo tee /etc/unified/unified-seed-node.env >/dev/null <<'EOF'
UNIFIED_RPC_HOST=127.0.0.1
UNIFIED_RPC_PORT=3337
EOF
```

```bash
sudo systemctl restart unified-seed-node
```

## 2. Install nginx and certificates

```bash
sudo apt-get update
sudo apt-get install -y nginx certbot python3-certbot-nginx
```

Point DNS for `rpc.yourdomain.example` at the node server, then issue the certificate:

```bash
sudo certbot certonly --nginx -d rpc.yourdomain.example
```

## 3. Install the proxy config

Copy [unified-rpc-proxy.conf.tmpl](/Users/efrainvera/Documents/UNIFIED/deploy/nginx/unified-rpc-proxy.conf.tmpl) to the server and replace:

- `__RPC_HOSTNAME__`
- `__SITE_ORIGIN__`
- `__TLS_CERT_FULLCHAIN__`
- `__TLS_CERT_PRIVKEY__`

Example:

```bash
sudo cp /path/to/unified-rpc-proxy.conf.tmpl /etc/nginx/sites-available/unified-rpc-proxy.conf
sudo nano /etc/nginx/sites-available/unified-rpc-proxy.conf
sudo ln -sf /etc/nginx/sites-available/unified-rpc-proxy.conf /etc/nginx/sites-enabled/unified-rpc-proxy.conf
sudo nginx -t
sudo systemctl reload nginx
```

## 4. Point the Pages site at the proxy

Edit [site-config.js](/Users/efrainvera/Documents/UNIFIED/web/site/assets/site-config.js):

```js
export const siteConfig = {
  siteName: "UniFied",
  defaultRpcUrl: "https://rpc.yourdomain.example/rpc",
  publicRpcPresets: [
    { label: "UniFied Mainnet", rpcUrl: "https://rpc.yourdomain.example/rpc" },
  ],
};
```

## 5. Verify

From any machine:

```bash
curl -s -X POST https://rpc.yourdomain.example/rpc \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"ufi_getNetworkConfig","params":{}}'
```

```bash
curl -s https://rpc.yourdomain.example/healthz
```

In the browser, the GitHub Pages site should connect without mixed-content or CORS errors.

## Why this shape

- GitHub Pages is static, so it cannot proxy requests itself.
- The site is HTTPS, so browser RPC must also be HTTPS.
- Keeping UniFied bound to `127.0.0.1:3337` avoids directly exposing the node process.
