# Testing

Unit tests cover the logic that fails silently in production:

- `listRequest` omits empty `Prefix`/`Delimiter` rather than sending `""` (OCI returns `400 InvalidObjectName` for empty query params).
- `isNotFound` treats only a 404 service error as missing, and never a transport error.
- `configurationProvider` rejects an unknown auth mode instead of silently defaulting.

```
go test ./...
```

## What unit tests cannot cover

Pagination and the end-to-end object round trip need a real bucket. Before trusting a release, run a real backup **and a real restore**:

```
velero backup create plugin-check --storage-location <oci-bsl> --wait
velero restore create --from-backup plugin-check --wait
```

Velero will start happily with a broken object store and fail on first object access, so a healthy pod proves nothing.
