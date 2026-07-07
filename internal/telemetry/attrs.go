package telemetry

// Application-specific attribute keys. Centralized here so handlers, storage
// backends, the cleanup service, and the database layer all share the same
// vocabulary on metrics.
//
// These are low-cardinality labels only. Identifying values (file IDs,
// filenames, client IPs) are deliberately never attached to metrics, so
// aggregates cannot be traced back to an individual request.
const (
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
