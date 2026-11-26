# CI/CD and releases

GitHub Actions is intentionally a thin runner: application checks, infrastructure
validation, releases, and deployment behavior live in `.mise.toml`. This makes
the commands runnable locally and in CI without separate shell-script logic.

## One-time AWS and GitHub setup

The account-level project at `../aws-accounts/micup-production` owns the S3
state bucket, GitHub OIDC provider, and GitHub deployment role. Apply it first
with your administrator credentials:

```bash
cd ../aws-accounts/micup-production
mise run apply
cd ../../server
mise run tofu:init
```

Confirm OpenTofu's migration prompt to move the existing
`infra/terraform.tfstate` into the encrypted, versioned S3 backend with S3
lockfiles. Do not commit a state file. No AWS access keys are stored in GitHub,
and this repository cannot modify the credential boundary that grants it
deployment access.

In GitHub, create a `production` environment, require the desired reviewers,
and set its `AWS_DEPLOY_ROLE_ARN` variable to that externally managed role ARN.
Restrict manual deployment and release workflows to maintainers.

## Workflows

- `CI` runs `mise run ci:verify`, `mise run release:check`, and
  `mise run ci:infra:validate`. Pull requests run the application and commit
  checks only, use no AWS credentials or repository secrets, and cancel stale
  runs when new commits arrive. The infrastructure job runs on pull requests
  and `main` pushes alike, with no cloud access: it initializes OpenTofu with
  `-backend=false` and runs `tofu validate`, so it needs no OIDC token or
  `production` environment.
- `Deploy production` is manual, waits for the protected environment approval,
  exchanges the GitHub OIDC token for the deploy role, then calls
  `mise run deploy:production`.
- `Release` is manual. Convco validates history, calculates the SemVer bump,
  generates notes, and the workflow creates the annotated `v*` tag and GitHub
  release. Run it only after the target commit is on `main`.

## Conventional commits

Use `convco commit` locally to create commits, and `mise run release:check` to
validate them. Convco maps `feat` to a minor release, `fix` to a patch release,
and breaking changes to a major release. Chores, CI, documentation, tests, and
refactors appear as non-version-bumping changes by default.

Three pre-existing nonconforming commits are retained as historical record, so
CI validates only each pull request or push range. The first release workflow
uses commit `922e623` as the baseline until the first `v*` release tag exists;
after that it validates changes since the latest release tag.

## Pull request protection

Create a GitHub ruleset for `main` that requires the three `CI` checks:
`Verify application`, `Validate conventional commits`, and `Validate
infrastructure`. Require at least one review and require branches to be current
before merging. Do not grant the pull-request workflow `id-token: write` or
write permissions; only the protected `production` deployment environment may
obtain AWS credentials.
