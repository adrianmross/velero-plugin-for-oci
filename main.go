// Velero ObjectStore plugin for OCI Object Storage.
//
// Talks to OCI's NATIVE Object Storage API, so it authenticates with OKE
// workload identity, instance principals, or a config file — and needs no
// Customer Secret Key.
//
// The usual way to back Velero with OCI is the AWS plugin pointed at OCI's
// S3-compatibility endpoint. That endpoint accepts only a Customer Secret Key,
// and a Customer Secret Key can only be issued against an IAM user, so it forces
// a long-lived static credential into the cluster. The native API has no such
// restriction.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/common/auth"
	"github.com/oracle/oci-go-sdk/v65/objectstorage"
	"github.com/sirupsen/logrus"
	"github.com/vmware-tanzu/velero/pkg/plugin/framework"
	veleroplugin "github.com/vmware-tanzu/velero/pkg/plugin/velero"
)

const pluginName = "red-wiz.stream/oci-object-store"

func main() {
	framework.NewServer().
		RegisterObjectStore(pluginName, newObjectStore).
		Serve()
}

func newObjectStore(logger logrus.FieldLogger) (any, error) {
	return &ObjectStore{log: logger}, nil
}

// ObjectStore implements velero.ObjectStore over OCI Object Storage.
type ObjectStore struct {
	log       logrus.FieldLogger
	client    objectstorage.ObjectStorageClient
	namespace string
}

// Compile-time guard: a Velero bump that changes the interface fails the build
// here rather than at plugin handshake time in the cluster.
var _ veleroplugin.ObjectStore = (*ObjectStore)(nil)

// Init builds the client from the BackupStorageLocation's spec.config.
//
// Recognised keys: region, namespace, auth ("workload_identity" | "instance_principal"
// | "config_file"). Everything is optional; namespace is resolved from the tenancy
// when omitted.
func (o *ObjectStore) Init(config map[string]string) error {
	provider, err := configurationProvider(config["auth"])
	if err != nil {
		return fmt.Errorf("oci auth: %w", err)
	}

	client, err := objectstorage.NewObjectStorageClientWithConfigurationProvider(provider)
	if err != nil {
		return fmt.Errorf("oci object storage client: %w", err)
	}
	if region := firstNonEmpty(config["region"], os.Getenv("OCI_REGION")); region != "" {
		client.SetRegion(region)
	}
	o.client = client

	o.namespace = firstNonEmpty(config["namespace"], os.Getenv("OCI_NAMESPACE"))
	if o.namespace == "" {
		resp, err := client.GetNamespace(context.Background(), objectstorage.GetNamespaceRequest{})
		if err != nil {
			return fmt.Errorf("resolve object storage namespace: %w", err)
		}
		o.namespace = *resp.Value
	}
	o.log.Infof("OCI object store ready (namespace=%s)", o.namespace)
	return nil
}

// configurationProvider resolves auth. Workload identity is the point of this
// plugin, so it is the default and is tried first.
//
// NOTE: oci-go-sdk's OKE workload-identity provider does NOT discover its own
// settings — it reads OCI_RESOURCE_PRINCIPAL_VERSION (must be "2.2") and
// OCI_RESOURCE_PRINCIPAL_REGION from the environment and fails hard without them.
// The Python SDK does discover them, so a call that works from a Python pod will
// fail here. Set both on the Velero server container.
func configurationProvider(mode string) (common.ConfigurationProvider, error) {
	switch strings.ToLower(mode) {
	case "workload_identity", "oke_workload_identity", "":
		p, err := auth.OkeWorkloadIdentityConfigurationProvider()
		if err == nil {
			return p, nil
		}
		if mode != "" {
			return nil, err
		}
		return common.DefaultConfigProvider(), nil
	case "instance_principal":
		return auth.InstancePrincipalConfigurationProvider()
	case "config_file", "default":
		return common.DefaultConfigProvider(), nil
	default:
		return nil, fmt.Errorf("unknown auth mode %q", mode)
	}
}

func (o *ObjectStore) PutObject(bucket, key string, body io.Reader) error {
	// io.Reader, not a buffer: Velero streams multi-GB backup tarballs through
	// here and holding one in memory would OOM the pod.
	_, err := o.client.PutObject(context.Background(), objectstorage.PutObjectRequest{
		NamespaceName: &o.namespace,
		BucketName:    &bucket,
		ObjectName:    &key,
		PutObjectBody: io.NopCloser(body),
	})
	if err != nil {
		return fmt.Errorf("put %s/%s: %w", bucket, key, err)
	}
	return nil
}

func (o *ObjectStore) ObjectExists(bucket, key string) (bool, error) {
	_, err := o.client.HeadObject(context.Background(), objectstorage.HeadObjectRequest{
		NamespaceName: &o.namespace,
		BucketName:    &bucket,
		ObjectName:    &key,
	})
	if err != nil {
		if isNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("head %s/%s: %w", bucket, key, err)
	}
	return true, nil
}

func (o *ObjectStore) GetObject(bucket, key string) (io.ReadCloser, error) {
	resp, err := o.client.GetObject(context.Background(), objectstorage.GetObjectRequest{
		NamespaceName: &o.namespace,
		BucketName:    &bucket,
		ObjectName:    &key,
	})
	if err != nil {
		return nil, fmt.Errorf("get %s/%s: %w", bucket, key, err)
	}
	return resp.Content, nil
}

// ListCommonPrefixes returns the "directories" one level below prefix. Velero
// uses it to enumerate backups, so a short answer silently hides backups from
// restore — hence the exhaustive paging.
func (o *ObjectStore) ListCommonPrefixes(bucket, prefix, delimiter string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	err := o.eachPage(bucket, prefix, delimiter, func(r objectstorage.ListObjectsResponse) {
		for _, p := range r.Prefixes {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	})
	if err != nil {
		return nil, fmt.Errorf("list prefixes %s/%s: %w", bucket, prefix, err)
	}
	return out, nil
}

func (o *ObjectStore) ListObjects(bucket, prefix string) ([]string, error) {
	var out []string
	err := o.eachPage(bucket, prefix, "", func(r objectstorage.ListObjectsResponse) {
		for _, obj := range r.Objects {
			if obj.Name != nil {
				out = append(out, *obj.Name)
			}
		}
	})
	if err != nil {
		return nil, fmt.Errorf("list objects %s/%s: %w", bucket, prefix, err)
	}
	return out, nil
}

// eachPage walks every page of a listing.
//
// This MUST be exhaustive. OCI returns an opaque NextStartWith cursor and caps a
// page at 1000; stopping at the first page makes older backups invisible to
// `velero backup get` and to restore, with no error anywhere. The equivalent bug
// bug in an S3 gateway backend broke every registry push and took a real push
// test to find; unit tests over a fake client will not catch it.
func (o *ObjectStore) eachPage(bucket, prefix, delimiter string, fn func(objectstorage.ListObjectsResponse)) error {
	limit := 1000
	var start *string
	for {
		resp, err := o.client.ListObjects(context.Background(),
			listRequest(o.namespace, bucket, prefix, delimiter, start, limit))
		if err != nil {
			return err
		}
		fn(resp)
		if resp.NextStartWith == nil || *resp.NextStartWith == "" {
			return nil
		}
		start = resp.NextStartWith
	}
}

// listRequest builds one page request.
//
// Extracted so the empty-parameter guard is testable: OCI rejects empty-string
// query parameters outright (400 InvalidObjectName, "the delimiter parameter ''
// is not supported"), so Prefix and Delimiter must be left nil rather than
// pointed at "". Velero calls ListObjects with an empty delimiter routinely.
func listRequest(namespace, bucket, prefix, delimiter string, start *string, limit int) objectstorage.ListObjectsRequest {
	req := objectstorage.ListObjectsRequest{
		NamespaceName: &namespace,
		BucketName:    &bucket,
		Start:         start,
		Limit:         &limit,
		Fields:        strPtr("name"),
	}
	if prefix != "" {
		req.Prefix = &prefix
	}
	if delimiter != "" {
		req.Delimiter = &delimiter
	}
	return req
}

func (o *ObjectStore) DeleteObject(bucket, key string) error {
	_, err := o.client.DeleteObject(context.Background(), objectstorage.DeleteObjectRequest{
		NamespaceName: &o.namespace,
		BucketName:    &bucket,
		ObjectName:    &key,
	})
	if err != nil {
		// Velero deletes expired backups; a missing object is success, not an
		// error, or expiry gets stuck retrying forever.
		if isNotFound(err) {
			return nil
		}
		return fmt.Errorf("delete %s/%s: %w", bucket, key, err)
	}
	return nil
}

// CreateSignedURL maps onto an OCI pre-authenticated request. Velero hands these
// to `velero backup download` / log fetches.
func (o *ObjectStore) CreateSignedURL(bucket, key string, ttl time.Duration) (string, error) {
	expiry := common.SDKTime{Time: time.Now().Add(ttl)}
	name := fmt.Sprintf("velero-%s-%d", strings.ReplaceAll(key, "/", "-"), time.Now().UnixNano())
	resp, err := o.client.CreatePreauthenticatedRequest(context.Background(), objectstorage.CreatePreauthenticatedRequestRequest{
		NamespaceName: &o.namespace,
		BucketName:    &bucket,
		CreatePreauthenticatedRequestDetails: objectstorage.CreatePreauthenticatedRequestDetails{
			Name:        &name,
			ObjectName:  &key,
			AccessType:  objectstorage.CreatePreauthenticatedRequestDetailsAccessTypeObjectread,
			TimeExpires: &expiry,
		},
	})
	if err != nil {
		return "", fmt.Errorf("create PAR %s/%s: %w", bucket, key, err)
	}
	// FullPath is host-qualified; AccessUri alone is a path and not usable as-is.
	if resp.FullPath == nil {
		return "", fmt.Errorf("create PAR %s/%s: no full path returned", bucket, key)
	}
	return *resp.FullPath, nil
}

func isNotFound(err error) bool {
	var svcErr common.ServiceError
	if se, ok := err.(common.ServiceError); ok {
		svcErr = se
		return svcErr.GetHTTPStatusCode() == 404
	}
	return false
}

func strPtr(s string) *string { return &s }

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
