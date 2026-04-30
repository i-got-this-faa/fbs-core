package s3

const (
	codeAccessDenied   = "AccessDenied"
	codeBadDigest      = "BadDigest"
	codeInternalError  = "InternalError"
	codeInvalidDigest  = "InvalidDigest"
	codeInvalidRequest = "InvalidRequest"
	codeNoSuchBucket   = "NoSuchBucket"
	codeNoSuchKey      = "NoSuchKey"
)

const (
	messageAccessDenied   = "Access denied."
	messageBadDigest      = "The Content-MD5 or checksum you specified did not match what we received."
	messageInternalError  = "We encountered an internal error. Please try again."
	messageInvalidDigest  = "The Content-MD5 you specified was invalid."
	messageInvalidRequest = "The request is invalid."
	messageNoSuchBucket   = "The specified bucket does not exist."
	messageNoSuchKey      = "The specified key does not exist."
)
