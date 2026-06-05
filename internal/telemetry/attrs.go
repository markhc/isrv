package telemetry

// Application-specific attribute keys. Centralized here so handlers, storage
// backends, the cleanup service, and the database layer all share the same
// vocabulary on spans and metrics. Keys are namespaced with "isrv." per the
// OpenTelemetry guidance for custom attributes.
const (
	// AttrFileID identifies a stored file by its public, generated ID.
	AttrFileID = "isrv.file.id"
	// AttrFileName is the original filename as supplied by the uploader.
	AttrFileName = "isrv.file.name"
	// AttrFileSize is the file size in bytes.
	AttrFileSize = "isrv.file.size_bytes"
	// AttrFileExpiration is the file's RFC3339-formatted expiration time.
	AttrFileExpiration = "isrv.file.expiration"

	// AttrRequestIP is the resolved client IP of the request.
	AttrRequestIP = "isrv.request.ip_address"

	// AttrCleanupExpiredCount is the number of expired files found in a
	// single cleanup cycle.
	AttrCleanupExpiredCount = "isrv.cleanup.expired_count"

	// AttrResult labels the outcome of an operation: success or error.
	AttrResult = "result"
	// AttrStorage labels the storage backend that handled the operation.
	AttrStorage = "storage"
	// AttrSource labels the actor that triggered a deletion: user or cleanup.
	AttrSource = "source"
	// AttrOperation labels a storage operation: save, delete, exists.
	AttrOperation = "operation"
	// AttrDecision labels a rate-limit decision: allow, throttle, block, blocked.
	AttrDecision = "decision"
)

// Conventional values for AttrResult.
const (
	ResultSuccess = "success"
	ResultError   = "error"
)
