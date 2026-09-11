# DevTrace

Contributor provenance and trust signals for GitHub projects.

DevTrace answers a question that is awkward to answer by hand: *what do we
actually know about the people contributing to this repository?* It gathers
public signals about a contributor — activity history across forges, package
publishing, security advisory credits, OSSF Scorecard results, bot
characteristics — and turns them into a scorecard you can look at before merging
a pull request from someone you have never met.

> **This is a reference implementation, not a product.** It is Apache-2.0
> licensed, self-hostable, and maintained on a best-effort basis. There is no
> SLA. See [CONTRIBUTING.md](CONTRIBUTING.md#project-governance) for what that
> means in practice.

## What it is not

DevTrace does not decide whether a person is trustworthy, and a low score is not
an accusation. Most low scores mean "we have little public information about
this account," which is the normal state for a new contributor and is not
evidence of anything. Treat the output as context for a human review, not as a
gate.

## Parts

| | |
|---|---|
| **DevTrace** (this repo) | The service: ingestion, scoring, dashboard, API |
| [devtrace-action](https://github.com/thingzio/devtrace-action) | GitHub Action that scores pull request contributors in CI |
| [devtrace-extension](https://github.com/thingzio/devtrace-extension) | Chrome extension showing scores inline on GitHub |

A hosted instance runs at [devtrace.thingz.io](https://devtrace.thingz.io). It
is a demo of this code, not a commercial service.

## Running it locally

You need Go (version in `go.mod`), Docker, and `make`. You do **not** need a
Google Cloud account or any credential from the maintainer.

```shell
git clone https://github.com/thingzio/devtrace && cd devtrace
make db-up     # local Postgres via docker compose
make seed      # seed a test tenant, prints an API token
make server    # run the dev server
```

Then open <http://localhost:8080>.

To run the full quality gate — the same one CI runs:

```shell
make qualify   # coverage, lint, govulncheck, e2e
```

`make help` lists every target.

> The local Postgres binds port **5432**. DevPulse and DevRadar do the same, so
> only run one of the three stacks at a time or you will get a confusing bind
> failure.

## How it works

A single binary, `devtrace-site`, serves HTTP and runs background workers in the
same process.

```
cmd/devtrace-site/   Entrypoint
pkg/server/          HTTP handlers, routing, rate limiting, templates
pkg/ingest/          GitHub data ingestion: GH Archive fetching, aggregation
pkg/background/      Ingestion scheduler and continuous scoring workers
pkg/score/           Scoring engine: heuristics, grade calculation
pkg/bot/             Bot detection
pkg/ossf/            OSSF Scorecard lookups
pkg/registry/        Package-registry publisher signals
pkg/stackoverflow/   Stack Exchange profile signals
pkg/forges/          Cross-VCS identity matching
pkg/claude/          Optional AI risk sensing
pkg/compliance/      NIST SSDF practice-to-signal mapping
pkg/tenant/          Tenants, sessions, API tokens, GitHub App installs
pkg/data/postgres/   PostgreSQL store and migrations
infra/run/           Terraform for the Cloud Run reference deployment
```

Signals are cached with per-source TTLs so a scorecard does not re-fetch
everything on every view, and the scorer pauses when the aggregate GitHub token
quota drops below a floor rather than burning the budget.

## Configuration

Everything is environment variables. `DATABASE_URL` is the only one strictly
required to boot.

| Variable | Purpose |
|---|---|
| `DATABASE_URL` | PostgreSQL connection URI — **required** |
| `BASE_URL` | Public base URL of this instance |
| `GITHUB_OAUTH_CLIENT_ID` / `_SECRET` | GitHub OAuth app, for sign-in |
| `GITHUB_APP_ID` / `GITHUB_APP_KEY_PATH` | GitHub App, for installation tokens |
| `GITHUB_WEBHOOK_SECRET` | HMAC secret for webhook verification |
| `DEVTRACE_ADMIN_USERS` | Comma-separated GitHub usernames granted `/admin` |
| `ANTHROPIC_API_KEY` | Optional; enables AI risk sensing |
| `DEVTRACE_DEBUG` | `true` for debug-level logging |

Self-hosters bring their own keys, so AI cost scales to whoever runs the
instance. The full list, including cache TTLs and scorer tuning, is in
[docs/DEVELOPMENT.md](docs/DEVELOPMENT.md).

**Running your own instance requires registering your own GitHub App** — the
maintainer's app cannot be shared. See [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md).

## Deploying

[docs/INFRA.md](docs/INFRA.md) covers the Terraform in `infra/run/`, which
provisions the whole stack on Google Cloud — Cloud Run, Cloud SQL, Secret
Manager, scheduling. It is turnkey but opinionated toward GCP; it is the
maintainer's reference deployment, not the only way to run this.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Documentation fixes are especially
welcome and are the easiest first contribution.

Security reports go through
[GitHub Security Advisories](https://github.com/thingzio/devtrace/security/advisories/new),
not public issues — see [SECURITY.md](SECURITY.md).

## License

[Apache 2.0](LICENSE). See [NOTICE](NOTICE) for attribution.
