// Package s3 implements port.Blob on S3-compatible object storage (MinIO in docker-compose). Document
// originals live under {tenant}/{bank}/docs/{document_id}/{content_hash}; DeletePrefix removes a whole
// bank's objects during delete_bank. Missing keys map to domain.ErrNotFound and transport failures to
// domain.ErrUnavailable. The adapter must pass the internal/porttest blob suite.
package s3
