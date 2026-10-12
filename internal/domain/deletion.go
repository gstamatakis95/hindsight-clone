package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// ReceiptStatus is the state of an org delete, as its deletion receipt records it.
type ReceiptStatus string

// Receipt statuses. The string is the deletion_receipts.status value.
const (
	// ReceiptStatusUnspecified is the zero value.
	ReceiptStatusUnspecified ReceiptStatus = ""
	// ReceiptStatusInProgress: the org is tombstoned and its delete is running.
	ReceiptStatusInProgress ReceiptStatus = "in_progress"
	// ReceiptStatusCompleted: every row and blob of the org is gone, and the verification found none left.
	ReceiptStatusCompleted ReceiptStatus = "completed"
	// ReceiptStatusFailedVerification: the last verification found rows or blobs left; the delete keeps retrying.
	ReceiptStatusFailedVerification ReceiptStatus = "failed_verification"
)

// DeletionReceipt is the record an org delete leaves behind. It names the org only by OrgIDHash and carries no
// bank ids, document ids, names or content, so it can outlive the org; retention prunes it.
type DeletionReceipt struct {
	// ID is the receipt id.
	ID string
	// Tenant is the tenant the org belonged to.
	Tenant string
	// OrgIDHash is OrgIDHash of the deleted org.
	OrgIDHash string
	// Status is the delete's state.
	Status ReceiptStatus
	// RequestedAt is when DeleteOrg tombstoned the org.
	RequestedAt time.Time
	// CompletedAt is when the verified delete finished; nil until then.
	CompletedAt *time.Time
	// Banks counts the banks DeleteOrg tombstoned.
	Banks int
	// Verification is the last verification's result; nil before the first one.
	Verification *ReceiptVerification
	// ServerVersion is the version of the server that last verified the delete.
	ServerVersion string
}

// ReceiptVerification is the result of one org delete verification.
type ReceiptVerification struct {
	// TablesChecked counts the tables carrying org_id that were counted.
	TablesChecked int `json:"tables_checked"`
	// RowsRemaining is the org's rows left over all those tables.
	RowsRemaining int64 `json:"rows_remaining"`
	// BlobsRemaining is 1 when a blob was still listed under the org's prefix, else 0 (the check lists one key).
	BlobsRemaining int64 `json:"blobs_remaining"`
	// VerifiedAt is when the verification ran.
	VerifiedAt time.Time `json:"verified_at"`
}

// OrgIDHash is the hex SHA-256 of the tenant id, a NUL byte and the org id: how a deletion receipt names its org.
// Whoever knows the org id can find the receipt, but the receipt alone does not show the id. A short id can still be
// guessed by hashing candidates, so the hash is pseudonymous, not anonymous.
func OrgIDHash(o OrgRef) string {
	h := sha256.New()
	h.Write([]byte(o.Tenant))
	h.Write([]byte{0})
	h.Write([]byte(o.Org))
	return hex.EncodeToString(h.Sum(nil))
}
