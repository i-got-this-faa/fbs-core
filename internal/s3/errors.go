package s3

const (
	codeBucketAlreadyExists       = "BucketAlreadyExists"
	codeBucketAlreadyOwnedByYou   = "BucketAlreadyOwnedByYou"
	codeAccessDenied              = "AccessDenied"
	codeBadDigest                 = "BadDigest"
	codeInternalError             = "InternalError"
	codeInvalidBucketName         = "InvalidBucketName"
	codeInvalidDigest             = "InvalidDigest"
	codeInvalidLocationConstraint = "InvalidLocationConstraint"
	codeInvalidRequest            = "InvalidRequest"
	codeMalformedXML              = "MalformedXML"
	codeNoSuchBucket              = "NoSuchBucket"
	codeNoSuchKey                 = "NoSuchKey"
)

const (
	messageBucketAlreadyExists       = "The requested bucket name is not available."
	messageBucketAlreadyOwnedByYou   = "Your previous request to create the named bucket succeeded and you already own it."
	messageAccessDenied              = "Access denied."
	messageBadDigest                 = "The Content-MD5 or checksum you specified did not match what we received."
	messageInternalError             = "We encountered an internal error. Please try again."
	messageInvalidBucketName         = "The specified bucket is not valid."
	messageInvalidDigest             = "The Content-MD5 you specified was invalid."
	messageInvalidLocationConstraint = "The specified location-constraint is not valid."
	messageInvalidRequest            = "The request is invalid."
	messageMalformedXML              = "The XML you provided was not well-formed or did not validate against our published schema."
	messageNoSuchBucket              = "The specified bucket does not exist."
	messageNoSuchKey                 = "The specified key does not exist."
)
