# benchy

The home of [benchy.run](https://benchy.run) — a personal benchmark for coding
agents. Capture a real coding session once, replay it against every agent you
use, and get one blind-judged composite score back.

This repo holds both halves of the product:

- **`cli/`** — the `benchy` Go CLI: prospective session capture, the portability
  filter, `benchy run`/`results`, and the blind judge. See [`cli/README.md`](cli/README.md).
- **root** (`src/`, `public/`, `wrangler.jsonc`, `schema.sql`) — the **benchy.run**
  Cloudflare Worker: landing page, `/install`, the leaderboard API (D1), and CLI
  binary distribution from R2 (`/dl/`, `/api/version`).

## Develop

```sh
# CLI
cd cli && go build ./... && go test ./...

# Site (benchy.run)
npx wrangler@latest dev        # local
npx wrangler@latest deploy     # prod
```

## Release the CLI

Distribution is via benchy.run/R2 (not GitHub releases):

1. Tag the version (`git tag v0.6.0 && git push origin v0.6.0`).
2. Build `benchy-<os>-<arch>` for darwin/linux × amd64/arm64 with
   `-ldflags "-s -w -X github.com/option-ai/benchy/cli/cmd.version=<tag>"`.
3. `wrangler r2 object put benchy-dl/<binary>` for each, plus a `latest.json`
   (`{"version":"<tag>"}`).
4. `wrangler deploy` the Worker.

`benchy update` and `benchy.run/install` both resolve benchy.run.
