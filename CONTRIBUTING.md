# Contributing to Quert

Thanks for your interest in contributing! This is a laid-back project - we're happy to help you get started and learn along the way.

## Code of Conduct

Keep it friendly:
- Be respectful and constructive
- Focus on the code, not the person
- Help others learn and improve
- Assume good intentions

## Getting Started

### What You'll Need

- Go 1.23.0+
- Git
- That's pretty much it!

### Quick Setup

1. Fork and clone:
```bash
git clone https://github.com/almahr1/quert.git
cd quert
```

2. Get dependencies:
```bash
go mod download
```

3. Make sure it works:
```bash
make test
```

That's it! If something doesn't work, just ask.

## How to Contribute

We welcome all kinds of contributions:
- Bug fixes
- New features
- Documentation improvements
- Performance optimizations
- Tests
- Code cleanup

Don't worry about being perfect - we're here to help!

## Making Changes

### Simple Process

1. Create a branch:
```bash
git checkout -b your-feature-name
```

2. Make your changes

3. Test if you can:
```bash
go fmt ./...      # Format all Go files in project
go build ./...    # Build all packages in project
go test ./...     # Test all packages in project. Use -v to see all individual testsz
```

4. Commit and push:
```bash
git add .
git commit -m "your change description"
git push origin your-feature-name
```

5. Open a pull request on GitHub

### Commit Messages

Keep it simple:
- `fix: something that was broken`
- `feat: something new`
- `docs: update documentation`
- `test: add tests`

Don't stress about the format too much.

## Code Guidelines

### Keep It Simple

Write clear Go code:
- Use descriptive names
- Handle errors properly
- Keep functions reasonably short
- Add comments for tricky parts

Example:
```go
func (n *URLNormalizer) NormalizeDomain(domain string) string {
    if domain == "" {
        return domain
    }
    return strings.ToLower(strings.TrimSpace(domain))
}
```

### Testing

Tests are helpful but not required for every tiny change:
- Add tests for new features when you can
- Bug fixes should include a test if possible
- Don't worry about perfect coverage

Run tests with:
```bash
make test           # All tests
make test-url       # Specific component
```

### Documentation

- Update docs if you add new features
- Add comments for complex code
- Don't stress about perfect documentation

## Project Layout

```
quert/
├── cmd/crawler/           # Main application
├── internal/
│   ├── config/           # Configuration
│   ├── client/           # HTTP client
│   ├── frontier/         # URL processing
│   └── ...               # Other components
```

## Getting Help

Stuck? No worries!

- Open an issue on GitHub
- Ask questions in pull requests
- Check existing docs (README.md, etc.)

## Development Commands

```bash
make test    # Run tests
make fmt     # Format code
make quick   # Format + test + build
```

---

Thanks for contributing! Every bit helps. 🎉
