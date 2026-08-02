# AGENTS.md

Conventions for AI coding agents working in this repo.

- `go vet ./...` and `go test ./...` must pass; the Dockerfile runs both before building, so a broken plugin fails the image build rather than Velero's plugin handshake.
- Keep `var _ veleroplugin.ObjectStore = (*ObjectStore)(nil)`. It turns a Velero interface change into a build failure instead of a runtime handshake failure.
- Do not add a dependency on any in-cluster S3 gateway or proxy. Backups must reach OCI directly; a backup path that depends on cluster infrastructure fails exactly when a restore is needed.
- Listing helpers must page exhaustively. A short listing hides backups with no error.
- Never send empty-string query parameters to OCI; leave them nil.
- Commit messages follow Conventional Commits.
