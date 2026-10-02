// Package iam defines the access-control vocabulary shared by every layer:
// user roles and S3 data-plane actions. It imports no internal package, so
// auth, authz, metadata, and the HTTP handlers can all depend on it.
package iam

import "errors"

// Role is a user's system-wide role.
type Role string

const (
	RoleAdmin  Role = "admin"
	RoleMember Role = "member"
)

// ErrInvalidRole is returned when a value is not a known Role.
var ErrInvalidRole = errors.New("role must be admin or member")

// ParseRole converts a stored or requested value into a Role.
func ParseRole(value string) (Role, error) {
	switch role := Role(value); role {
	case RoleAdmin, RoleMember:
		return role, nil
	default:
		return "", ErrInvalidRole
	}
}

// Action is an S3 data-plane action name. Handlers map protocol operations
// to these names; grants and the evaluator speak only these names.
type Action string

const (
	ActionCreateBucket             Action = "s3:CreateBucket"
	ActionDeleteBucket             Action = "s3:DeleteBucket"
	ActionListBucket               Action = "s3:ListBucket"
	ActionGetObject                Action = "s3:GetObject"
	ActionPutObject                Action = "s3:PutObject"
	ActionDeleteObject             Action = "s3:DeleteObject"
	ActionListMultipartUploadParts Action = "s3:ListMultipartUploadParts"
	ActionAbortMultipartUpload     Action = "s3:AbortMultipartUpload"
)

// ErrNotGrantable is returned when a value is not an action that a grant may hold.
var ErrNotGrantable = errors.New("invalid or non-grantable action")

// grantableActions excludes bucket creation and deletion, which only admins
// and bucket owners may perform.
var grantableActions = []Action{
	ActionListBucket,
	ActionGetObject,
	ActionPutObject,
	ActionDeleteObject,
	ActionListMultipartUploadParts,
	ActionAbortMultipartUpload,
}

// IsGrantable reports whether the action may appear on a grant.
func (a Action) IsGrantable() bool {
	for _, grantable := range grantableActions {
		if a == grantable {
			return true
		}
	}
	return false
}

// ParseGrantableAction converts a requested value into a grantable Action.
func ParseGrantableAction(value string) (Action, error) {
	action := Action(value)
	if !action.IsGrantable() {
		return "", ErrNotGrantable
	}
	return action, nil
}
