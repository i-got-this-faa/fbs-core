package authz

// Grant is the authorization view of a persisted resource grant.
// Metadata repositories map store rows into this shape.
type Grant struct {
	BucketName    string
	GranteeUserID string
	Action        string
	KeyPrefix     string
	Active        bool
}

// PrefixMatches reports whether objectKey is covered by grantPrefix.
// Empty grantPrefix matches the entire keyspace. Matching is a literal string
// prefix: key equals prefix, or key starts with prefix.
func PrefixMatches(grantPrefix, objectKey string) bool {
	if grantPrefix == "" {
		return true
	}
	if objectKey == grantPrefix {
		return true
	}
	return len(objectKey) > len(grantPrefix) && objectKey[:len(grantPrefix)] == grantPrefix
}

// ListPrefixCovered reports whether a ListBucket request prefix is covered by
// a grant prefix. Callers may narrow into a subtree they can already list, not
// widen outside it.
//
// Covered means:
//   - grant prefix is empty (whole-bucket list grant), or
//   - request prefix equals grant prefix, or
//   - request prefix extends grant prefix (starts with grant prefix)
//
// An empty request prefix is only covered by an empty grant prefix.
func ListPrefixCovered(grantPrefix, requestPrefix string) bool {
	if grantPrefix == "" {
		return true
	}
	if requestPrefix == "" {
		return false
	}
	if requestPrefix == grantPrefix {
		return true
	}
	return len(requestPrefix) > len(grantPrefix) && requestPrefix[:len(grantPrefix)] == grantPrefix
}
