# GitHub Pages Deployment

UniFied's public site in [web/site](/Users/efrainvera/Documents/UNIFIED/web/site) is static and can be published directly to GitHub Pages.

## Constraint

GitHub Pages cannot proxy `/rpc`, `/ws`, `/healthz`, or the REST endpoints from your node. The live site therefore needs a separate public HTTPS endpoint that proxies to a UniFied node.

Required:

- `https://.../rpc`
- `wss://.../ws`
- browser CORS enabled for the GitHub Pages origin

Recommended:

- `https://.../healthz`
- `https://.../p2p/peers`
- `https://.../chain/status`
- `https://.../governance/rules`

## Configure the Site

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

If `defaultRpcUrl` is blank, the site will ask the user for an RPC endpoint instead of assuming GitHub Pages can serve `/rpc`.

## Publish Flow

The workflow is in [pages.yml](/Users/efrainvera/Documents/UNIFIED/.github/workflows/pages.yml). It uploads `web/site` directly to GitHub Pages.

1. Push the repo to GitHub.
2. In GitHub, open `Settings -> Pages`.
3. Set the source to `GitHub Actions`.
4. Push to `main` or run the workflow manually.

For the required HTTPS reverse proxy in front of a live node, use [github-pages-rpc-proxy.md](/Users/efrainvera/Documents/UNIFIED/docs/github-pages-rpc-proxy.md).

## Notes

- [\.nojekyll](/Users/efrainvera/Documents/UNIFIED/web/site/.nojekyll) is included so Pages serves the site exactly as-is.
- All site links and assets are relative, so both user pages and project pages work.
- If you publish the site over HTTPS, the RPC endpoint must also be HTTPS or the browser will block it as mixed content.
