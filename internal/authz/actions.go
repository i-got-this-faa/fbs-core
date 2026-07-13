package authz

// Action names for S3 data-plane authorization. Handlers map protocol
// operations to these names; grants and the evaluator speak only these names.
const (
	ActionCreateBucket             = "s3:CreateBucket"
	ActionDeleteBucket             = "s3:DeleteBucket"
	ActionListBucket               = "s3:ListBucket"
	ActionGetObject                = "s3:GetObject"
	ActionPutObject                = "s3:PutObject"
	ActionDeleteObject             = "s3:DeleteObject"
	ActionListMultipartUploadParts = "s3:ListMultipartUploadParts"
	ActionAbortMultipartUpload     = "s3:AbortMultipartUpload"
)

// GrantableActions is the set accepted by grant create/update APIs.
var GrantableActions = map[string]struct{}{
	ActionListBucket:               {},
	ActionGetObject:                {},
	ActionPutObject:                {},
	ActionDeleteObject:             {},
	ActionListMultipartUploadParts: {},
	ActionAbortMultipartUpload:     {},
}

// IsGrantable reports whether action may appear on a grant row.
func IsGrantable(action string) bool {
	_, ok := GrantableActions[action]
	return ok
}

// AllDataPlaneActions lists every action the evaluator understands for S3.
var AllDataPlaneActions = []string{
	ActionCreateBucket,
	ActionDeleteBucket,
	ActionListBucket,
	ActionGetObject,
	ActionPutObject,
	ActionDeleteObject,
	ActionListMultipartUploadParts,
	ActionAbortMultipartUpload,
}
