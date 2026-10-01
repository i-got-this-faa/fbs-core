package iam

import (
	"errors"
	"testing"
)

func TestParseRole(t *testing.T) {
	for _, value := range []string{"admin", "member"} {
		if role, err := ParseRole(value); err != nil || string(role) != value {
			t.Errorf("ParseRole(%q) = %q, %v", value, role, err)
		}
	}
	for _, value := range []string{"", "Admin", "owner", " admin"} {
		if _, err := ParseRole(value); !errors.Is(err, ErrInvalidRole) {
			t.Errorf("ParseRole(%q) err = %v, want ErrInvalidRole", value, err)
		}
	}
}

func TestParseGrantableAction(t *testing.T) {
	for _, action := range grantableActions {
		if got, err := ParseGrantableAction(string(action)); err != nil || got != action {
			t.Errorf("ParseGrantableAction(%q) = %q, %v", action, got, err)
		}
	}
	for _, value := range []string{"", "s3:CreateBucket", "s3:DeleteBucket", "s3:getobject", "s3:*"} {
		if _, err := ParseGrantableAction(value); !errors.Is(err, ErrNotGrantable) {
			t.Errorf("ParseGrantableAction(%q) err = %v, want ErrNotGrantable", value, err)
		}
	}
}
