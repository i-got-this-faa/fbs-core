package s3

import "errors"

const (
	codeBucketAlreadyExists       = "BucketAlreadyExists"
	codeBucketAlreadyOwnedByYou   = "BucketAlreadyOwnedByYou"
	codeBucketNotEmpty            = "BucketNotEmpty"
	codeAccessDenied              = "AccessDenied"
	codeBadDigest                 = "BadDigest"
	codeEntityTooSmall            = "EntityTooSmall"
	codeInternalError             = "InternalError"
	codeInvalidArgument           = "InvalidArgument"
	codeInvalidBucketName         = "InvalidBucketName"
	codeInvalidDigest             = "InvalidDigest"
	codeInvalidLocationConstraint = "InvalidLocationConstraint"
	codeInvalidPart               = "InvalidPart"
	codeInvalidPartOrder          = "InvalidPartOrder"
	codeInvalidRange              = "InvalidRange"
	codeInvalidRequest            = "InvalidRequest"
	codeMalformedXML              = "MalformedXML"
	codeNoSuchBucket              = "NoSuchBucket"
	codeNoSuchKey                 = "NoSuchKey"
	codeNoSuchUpload              = "NoSuchUpload"
	codeNotImplemented            = "NotImplemented"
	codePreconditionFailed        = "PreconditionFailed"
)

var errRangeExceedsSize = errors.New("range exceeds object size")

const (
	messageBucketAlreadyExists       = "The requested bucket name is not available."
	messageBucketAlreadyOwnedByYou   = "Your previous request to create the named bucket succeeded and you already own it."
	messageBucketNotEmpty            = "The bucket you tried to delete is not empty."
	messageAccessDenied              = "Access denied."
	messageBadDigest                 = "The Content-MD5 or checksum you specified did not match what we received."
	messageEntityTooSmall            = "Your proposed upload is smaller than the minimum allowed object size."
	messageInternalError             = "We encountered an internal error. Please try again."
	messageInvalidArgument           = "Invalid argument."
	messageInvalidBucketName         = "The specified bucket is not valid."
	messageInvalidDigest             = "The Content-MD5 you specified was invalid."
	messageInvalidLocationConstraint = "The specified location-constraint is not valid."
	messageInvalidPart               = "One or more of the specified parts could not be found or the specified entity tag might not have matched the part's entity tag."
	messageInvalidPartOrder          = "The list of parts was not in ascending order. Parts must be ordered by part number."
	messageInvalidRequest            = "The request is invalid."
	messageMalformedXML              = "The XML you provided was not well-formed or did not validate against our published schema."
	messageNoSuchBucket              = "The specified bucket does not exist."
	messageNoSuchKey                 = "The specified key does not exist."
	messageNotImplemented            = "A header or query you provided implies functionality that is not implemented."
	messageNoSuchUpload              = "The specified multipart upload does not exist."
	messagePreconditionFailed        = "At least one of the pre-conditions you specified did not hold."
)

var errInvalidMaxKeys = errors.New("invalid max-keys")
