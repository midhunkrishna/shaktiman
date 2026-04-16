# Cloudflare Pages deployment

The documentation site deploys to Cloudflare Pages via the dashboard's Git integration.
Push-to-deploy; PRs get preview URLs automatically. There is **no CI workflow** in
this repo for the site — the Pages build system invokes `npm run build` on every push.

## One-time setup (maintainer)

1. **Cloudflare dashboard → Workers & Pages → Create → Pages → Connect to Git.**
2. Select the `midhunkrishna/shaktiman` repo. Authorize the Cloudflare Pages GitHub
   app if prompted.
3. **Framework preset:** Docusaurus.
4. **Build settings** (confirm / override the preset):

   | Setting | Value |
   |---|---|
   | Build command | `npm run build` |
   | Build output directory | `website/build` |
   | Root directory (advanced) | `website` |

5. **Environment variables** (Settings → Environment variables):

   | Variable | Value |
   |---|---|
   | `NODE_VERSION` | `20` |

   (The `website/.nvmrc` pins Node 20 too, but the env var is what Cloudflare's build
   image actually honours.)

6. **Production branch:** `master`. Non-production branches automatically produce
   preview deployments at `https://<pr-number>.<project>.pages.dev`.

7. Trigger the first deploy by pushing to `master` (or by clicking **Retry deployment**
   on the first automatically-queued build).

## Optional: custom domain

After the first successful production deploy, add a custom domain under **Custom
domains**. Cloudflare provisions TLS automatically.

Once the custom domain is live, update `website/docusaurus.config.ts` so `url` points
at it — this matters for canonical URLs, sitemap, and (future) Algolia DocSearch.

## Gotchas

- **Do not add a `/* /index.html 200` rule in `_redirects`.** Docusaurus emits a real
  `build/404.html` which Cloudflare Pages uses natively. Adding an SPA fallback makes
  Cloudflare's build system flag it as an infinite loop and ignore it.
- **`baseUrl: '/'`** in `docusaurus.config.ts` — keep it as-is for a root-domain
  deploy. If you later host under a path (`/docs/`, etc.), change it to match.
- **Yarn build cache** on Cloudflare is Yarn-1-only. We use npm, so this doesn't apply
  here — but don't switch to Yarn Berry / pnpm without expecting cold-build latency.
- **`onBrokenLinks: 'throw'`** is set in `docusaurus.config.ts` — any broken internal
  link fails the Pages build. Fix locally with `npm run build` before pushing.

## Verifying a deployment

- Production URL is `https://<project>.pages.dev` (or your custom domain).
- Preview URL for a PR is posted as a comment by the Cloudflare Pages GitHub app —
  usually within 2–3 minutes of the push.
- Sanity-check a preview by loading a known page, clicking through the sidebar, and
  hitting `/does-not-exist` — the `404.html` should render.
