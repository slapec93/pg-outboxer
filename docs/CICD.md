# CI/CD Pipeline Documentation

## Overview

The pg-outboxer project uses GitHub Actions for continuous integration and deployment. The pipeline automates testing, building, and releasing across multiple workflows.

## Workflows

### 1. CI Workflow (`.github/workflows/ci.yml`)

**Triggers:**
- Push to `main` branch
- Pull requests targeting `main`

**Jobs:**

#### Lint
- Runs golangci-lint with comprehensive checks
- Validates code style and catches common issues
- Fails on any linting errors

#### Unit Tests
- Runs all unit tests with race detection
- Generates coverage report
- Uploads coverage to Codecov (optional)
- Uses `go test -v -race -coverprofile=coverage.out ./...`

#### Integration Tests
- **Only runs on:**
  - Main branch pushes
  - PRs with `integration-test` label
- Spins up real PostgreSQL containers using testcontainers
- Tests full end-to-end flows
- Takes ~11 seconds to complete all 5 tests

#### Build
- Compiles the binary for Linux/AMD64
- Verifies binary works (`./pg-outboxer version`)
- Uploads artifact for download

#### Docker Build
- Builds Docker image without pushing
- Uses BuildKit cache for faster builds
- Tests image by running `version` command

**Status Checks:**
All jobs except integration tests are required for PR merge.

### 2. Release Workflow (`.github/workflows/release.yml`)

**Triggers:**
- Git tags matching `v*` (e.g., `v0.1.0`, `v1.2.3`)

**Jobs:**

#### Release (GoReleaser)
- Builds binaries for multiple platforms:
  - Linux: amd64, arm64
  - macOS: amd64, arm64
  - Windows: amd64, arm64
- Creates archives (tar.gz for Unix, zip for Windows)
- Generates checksums
- Creates GitHub release with changelog
- Uploads all artifacts

**Platforms:**
```
pg-outboxer_0.1.0_Linux_x86_64.tar.gz
pg-outboxer_0.1.0_Linux_arm64.tar.gz
pg-outboxer_0.1.0_Darwin_x86_64.tar.gz
pg-outboxer_0.1.0_Darwin_arm64.tar.gz
pg-outboxer_0.1.0_Windows_x86_64.zip
pg-outboxer_0.1.0_Windows_arm64.zip
```

#### Docker Release
- Builds multi-platform images (amd64 + arm64)
- Pushes to GitHub Container Registry (ghcr.io)
- Tags with:
  - Full semver: `v1.2.3`
  - Minor: `v1.2`
  - Major: `v1`
  - Git SHA: `sha-abc123`
- Uses layer caching for faster builds

**Image:**
```
ghcr.io/slapec93/pg-outboxer:v0.1.0
ghcr.io/slapec93/pg-outboxer:v0.1
ghcr.io/slapec93/pg-outboxer:v0
ghcr.io/slapec93/pg-outboxer:sha-abc123
```

### 3. Dependabot (`.github/dependabot.yml`)

**Automated Dependency Updates:**
- **Go modules**: Weekly on Monday
- **GitHub Actions**: Weekly on Monday
- **Docker base images**: Weekly on Monday

Opens PRs automatically with:
- Changelog links
- Compatibility info
- Release notes

## Configuration Files

### `.goreleaser.yml`
Defines how releases are built:
- Build configurations for each platform
- Archive formats and contents
- Changelog generation rules
- Release notes templates
- Package manager integrations (Homebrew, apt, yum)

### `.golangci.yml`
Linter configuration:
- Enabled linters (errcheck, gosimple, govet, etc.)
- Custom rules and exclusions
- Security checks (gosec)
- Style checks (revive, stylecheck)

## Local Development

### Run Tests Locally
```bash
# Unit tests
make test

# Integration tests (requires Docker)
make test-integration

# All tests
make test-all
```

### Run Linter Locally
Install golangci-lint:
```bash
# macOS
brew install golangci-lint

# Linux
curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(go env GOPATH)/bin

# Windows
# Download from https://github.com/golangci/golangci-lint/releases
```

Run linter:
```bash
make lint
```

### Test Release Locally
```bash
# Install goreleaser
brew install goreleaser

# Test release build (no publish)
goreleaser release --snapshot --clean
```

## Creating a Release

### 1. Update Version
Update version references if needed (usually automatic).

### 2. Create and Push Tag
```bash
git tag -a v0.2.0 -m "Release v0.2.0: Add CDC support"
git push origin v0.2.0
```

### 3. Verify Release
1. Check [GitHub Actions](https://github.com/slapec93/pg-outboxer/actions)
2. Wait for green checkmarks
3. View [Releases](https://github.com/slapec93/pg-outboxer/releases)
4. Verify binaries and Docker images

### 4. Test Docker Image
```bash
docker pull ghcr.io/slapec93/pg-outboxer:v0.2.0
docker run --rm ghcr.io/slapec93/pg-outboxer:v0.2.0 version
```

## Troubleshooting

### CI Failing on Integration Tests
Integration tests require Docker and can be slow. If flaky:
1. Add `integration-test` label to PR to run them
2. Check if testcontainers can pull postgres:15-alpine
3. Look for timeout issues in test logs

### Release Failing
Common issues:
- **Tag format**: Must match `v*` pattern (e.g., `v1.0.0`)
- **GITHUB_TOKEN**: Should have `contents: write` permission
- **Docker build**: Check if Dockerfile is valid
- **GoReleaser**: Validate `.goreleaser.yml` locally first

### Dependabot PRs Failing
- Review the dependency change
- Run tests locally with the new version
- Check for breaking changes in changelogs
- Update code if needed, then merge

## Security

### Secrets Management
No secrets stored in code. Required secrets:
- `GITHUB_TOKEN`: Auto-provided by GitHub Actions
- `CODECOV_TOKEN`: Optional, for coverage reports

### Docker Image Scanning
Consider adding Trivy or Snyk scanning:
```yaml
- name: Run Trivy vulnerability scanner
  uses: aquasecurity/trivy-action@master
  with:
    image-ref: ghcr.io/slapec93/pg-outboxer:${{ github.sha }}
    format: 'sarif'
    output: 'trivy-results.sarif'
```

## Metrics

### Build Times (Approximate)
- Lint: ~30s
- Unit Tests: ~15s
- Integration Tests: ~20s
- Binary Build: ~45s
- Docker Build: ~2m (first), ~30s (cached)
- Full Release: ~5m

### Coverage Targets
- Unit tests: >80%
- Integration tests: >90% of critical paths

## Future Improvements

- [ ] Add SBOM generation
- [ ] Container vulnerability scanning
- [ ] Performance benchmarking in CI
- [ ] E2E tests in staging environment
- [ ] Canary releases
- [ ] Automatic rollback on failure
