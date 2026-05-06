# Contributing

## Security

Found a security vulnerability? Do not open a GitHub issue. See [SECURITY.md](SECURITY.md) for responsible disclosure guidelines.

## Code Style

- Run `gofmt` or `goimports` before committing
- Max line length: 100 characters
- camelCase for Go identifiers, snake_case for config keys
- Write tests for new functionality

## Workflow

1. Create a feature branch: `git checkout -b feature/my-feature`
2. Make your changes
3. Commit with GPG signature and conventional commits format
4. Push and open a pull request

## Commit Messages

GPG-sign all commits (`git commit -S`) and follow conventional commits:

- `feat:` - New feature
- `fix:` - Bug fix
- `docs:` - Documentation
- `test:` - Tests
- `refactor:` - Code refactoring
- `chore:` - Build, dependencies, etc.

Enable automatic signing: `git config commit.gpgsign true`

## Testing

All new features and bug fixes must include automated tests. PRs without tests will not be merged.

## Questions

Open an issue in the repository.
