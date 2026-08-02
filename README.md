# velero-plugin-for-oci

A [Velero](https://velero.io) `ObjectStore` plugin for **Oracle Cloud Infrastructure Object Storage**, using OCI's **native** API.

## Why

The usual way to point Velero at OCI is the AWS plugin against OCI's S3-compatibility endpoint. That endpoint authenticates **only** with a Customer Secret Key, and a Customer Secret Key can only be issued against an IAM user — so it forces a long-lived static credential into your cluster.

OCI's native Object Storage API has no such restriction. This plugin uses it, so Velero can authenticate with:

- **OKE workload identity** (default) — no stored credential at all
- **instance principals**
- a local config file, for development

It also means backups do not depend on any in-cluster S3 gateway. A backup system should not rely on infrastructure that may be unavailable at precisely the moment you need a restore.

## Install

Add the plugin as an initContainer on the Velero deployment:

```yaml
initContainers:
  - name: velero-plugin-for-oci
    image: ghcr.io/adrianmross/velero-plugin-for-oci:v0.1.0
    imagePullPolicy: IfNotPresent
    volumeMounts:
      - mountPath: /target
        name: plugins
```

`oci-go-sdk`'s workload-identity provider reads its settings from the environment and **does not discover them** the way the Python SDK does. Set both on the Velero server container:

```yaml
env:
  - name: OCI_RESOURCE_PRINCIPAL_VERSION
    value: "2.2"
  - name: OCI_RESOURCE_PRINCIPAL_REGION
    value: <bucket region>
```

Then declare a `BackupStorageLocation`:

```yaml
provider: red-wiz.stream/oci-object-store
bucket: my-velero-bucket
config:
  region: us-sanjose-1
  namespace: mytenancy      # optional; resolved from the tenancy if omitted
  auth: workload_identity   # or instance_principal / config_file
```

## IAM policy

Grant the workload principal access to the backup bucket only:

```
Allow any-user to read buckets in compartment <c> where all {
  request.principal.type = 'workload',
  request.principal.namespace = 'velero',
  request.principal.service_account = 'velero-server',
  request.principal.cluster_id = '<cluster ocid>',
  target.bucket.name = '<bucket>' }

Allow any-user to manage objects in compartment <c> where all { ...same..., target.bucket.name = '<bucket>' }
```

## Notes from building it

Three things are easy to get wrong and fail silently rather than loudly:

- **Listings must page exhaustively.** OCI returns an opaque `NextStartWith` cursor and caps a page at 1000. Stopping at the first page makes older backups invisible to `velero backup get` *and* to restore, with no error anywhere.
- **Empty query parameters must be omitted, not sent as `""`.** OCI rejects them with `400 InvalidObjectName`. Velero calls `ListObjects` with an empty delimiter routinely, so passing `""` through breaks every listing.
- **`DeleteObject` should treat 404 as success**, or backup expiry retries forever — but a non-service error must not be swallowed as "missing", or Velero considers a backup reclaimed when it was not.

## License

MIT
