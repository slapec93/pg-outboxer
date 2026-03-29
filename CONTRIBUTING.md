# Contributing to pg-outboxer

Thank you for your interest in contributing! This guide will help you get started.

## Development Setup

### Prerequisites
- Go 1.22 or later
- Docker (for integration tests)
- Make

### Clone and Setup
```bash
git clone https://github.com/slapec93/pg-outboxer.git
cd pg-outboxer
go mod download
```

### Build
```bash
make build
./pg-outboxer version
```

## Development Workflow

### 1. Create a Branch
```bash
git checkout -b feature/your-feature-name
# or
git checkout -b fix/your-bug-fix
```

### 2. Make Changes
Follow these guidelines:
- Write clear, self-documenting code
- Add tests for new functionality
- Update documentation as needed
- Follow Go conventions and idioms

### 3. Run Tests
```bash
# Unit tests
make test

# Integration tests (requires Docker)
make test-integration

# All tests
make test-all
```

### 4. Run Linter
```bash
make lint

# Auto-fix formatting issues
make fmt
```

### 5. Commit Your Changes
Use conventional commit messages:
```bash
git commit -m "feat: add Redis publisher support"
git commit -m "fix: handle nil pointer in webhook publisher"
git commit -m "docs: update configuration examples"
git commit -m "test: add integration test for retry logic"
```

Commit message prefixes:
- `feat:` - New feature
- `fix:` - Bug fix
- `docs:` - Documentation only
- `test:` - Adding or updating tests
- `refactor:` - Code refactoring
- `perf:` - Performance improvement
- `chore:` - Maintenance tasks
- `ci:` - CI/CD changes

### 6. Push and Create PR
```bash
git push origin feature/your-feature-name
```

Then open a pull request on GitHub.

## Code Style

### Go Best Practices
- Follow [Effective Go](https://go.dev/doc/effective_go)
- Use `gofmt` for formatting
- Run `golangci-lint` before committing
- Keep functions small and focused
- Write descriptive variable names
- Add comments for exported functions

### Project Structure
```
pg-outboxer/
├── cmd/pg-outboxer/      # CLI commands
├── internal/             # Private application code
│   ├── config/          # Configuration loading
│   ├── source/          # Event sources (polling, CDC)
│   ├── publisher/       # Event publishers (webhook, etc)
│   ├── delivery/        # Delivery orchestration
│   └── metrics/         # Prometheus metrics
├── test/integration/    # Integration tests
└── deployments/         # Deployment configs
```

## Testing Guidelines

### Unit Tests
- Test files should be named `*_test.go`
- Use table-driven tests where appropriate
- Mock external dependencies
- Aim for >80% coverage

Example:
```go
func TestMyFunction(t *testing.T) {
    tests := []struct {
        name    string
        input   string
        want    string
        wantErr bool
    }{
        {"valid input", "foo", "bar", false},
        {"invalid input", "", "", true},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got, err := MyFunction(tt.input)
            if (err != nil) != tt.wantErr {
                t.Errorf("unexpected error: %v", err)
            }
            if got != tt.want {
                t.Errorf("got %v, want %v", got, tt.want)
            }
        })
    }
}
```

### Integration Tests
- Use build tag `//go:build integration`
- Test real database interactions
- Use testcontainers for dependencies
- Clean up resources properly

## Pull Request Process

1. **Update documentation** - If you change behavior, update README.md
2. **Add tests** - New features need tests
3. **Pass CI checks** - All tests and linters must pass
4. **Get review** - Wait for maintainer review
5. **Address feedback** - Make requested changes
6. **Merge** - Maintainer will merge when ready

## CI/CD Pipeline

### On Pull Request
- ✅ Linting
- ✅ Unit tests
- ✅ Build verification
- ✅ Docker build

### On Main Branch
- ✅ All PR checks
- ✅ Integration tests
- ✅ Coverage report

### On Tag (v*)
- ✅ Build binaries for all platforms
- ✅ Create GitHub release
- ✅ Build and push Docker images
- ✅ Update package managers

## Release Process

Releases are automated via GitHub Actions:

1. Update version in code if needed
2. Create and push a tag:
   ```bash
   git tag -a v0.2.0 -m "Release v0.2.0"
   git push origin v0.2.0
   ```
3. GitHub Actions will:
   - Build binaries for all platforms
   - Create GitHub release with changelog
   - Build and push Docker images to ghcr.io
   - Update Homebrew tap (if configured)

## Getting Help

- 💬 Open a [Discussion](https://github.com/slapec93/pg-outboxer/discussions) for questions
- 🐛 Open an [Issue](https://github.com/slapec93/pg-outboxer/issues) for bugs
- 📧 Contact maintainers for security issues

## Code of Conduct

Be respectful, inclusive, and professional. We're all here to learn and build together.

## License

By contributing, you agree that your contributions will be licensed under the MIT License.
