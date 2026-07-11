package authz_test

import (
	"context"
	"errors"
	"testing"

	"github.com/i-got-this-faa/fbs/internal/auth"
	"github.com/i-got-this-faa/fbs/internal/authz"
)

type staticGrantStore struct {
	grants []authz.Grant
	err    error
}

func (s staticGrantStore) ListActiveForGranteeBucket(_ context.Context, granteeUserID, bucketName string) ([]authz.Grant, error) {
	if s.err != nil {
		return nil, s.err
	}
	var out []authz.Grant
	for _, g := range s.grants {
		if g.GranteeUserID == granteeUserID && g.BucketName == bucketName && g.Active {
			out = append(out, g)
		}
	}
	return out, nil
}

func TestEvaluatorAdminAllow(t *testing.T) {
	t.Parallel()
	eval := &authz.Evaluator{Grants: staticGrantStore{}}
	ok, err := eval.Allow(context.Background(), authz.DecisionRequest{
		Principal:     auth.Principal{UserID: "admin", Role: "admin"},
		Action:        authz.ActionGetObject,
		Bucket:        "any",
		ObjectKey:     "k",
		BucketOwnerID: "other",
	})
	if err != nil || !ok {
		t.Fatalf("allow = %v err = %v, want true", ok, err)
	}
}

func TestEvaluatorOwnerAllow(t *testing.T) {
	t.Parallel()
	eval := &authz.Evaluator{Grants: staticGrantStore{}}
	ok, err := eval.Allow(context.Background(), authz.DecisionRequest{
		Principal:     auth.Principal{UserID: "owner", Role: "member"},
		Action:        authz.ActionDeleteBucket,
		Bucket:        "b",
		BucketOwnerID: "owner",
	})
	if err != nil || !ok {
		t.Fatalf("allow = %v err = %v, want true", ok, err)
	}
}

func TestEvaluatorOwnerDenyForeignWithoutGrant(t *testing.T) {
	t.Parallel()
	eval := &authz.Evaluator{Grants: staticGrantStore{}}
	ok, err := eval.Allow(context.Background(), authz.DecisionRequest{
		Principal:     auth.Principal{UserID: "member", Role: "member"},
		Action:        authz.ActionGetObject,
		Bucket:        "b",
		ObjectKey:     "k",
		BucketOwnerID: "other",
	})
	if err != nil || ok {
		t.Fatalf("allow = %v err = %v, want false", ok, err)
	}
}

func TestEvaluatorGranteeAllowExactAction(t *testing.T) {
	t.Parallel()
	eval := &authz.Evaluator{Grants: staticGrantStore{grants: []authz.Grant{{
		BucketName: "b", GranteeUserID: "g", Action: authz.ActionGetObject, KeyPrefix: "", Active: true,
	}}}}
	ok, err := eval.Allow(context.Background(), authz.DecisionRequest{
		Principal:     auth.Principal{UserID: "g", Role: "member"},
		Action:        authz.ActionGetObject,
		Bucket:        "b",
		ObjectKey:     "docs/a.txt",
		BucketOwnerID: "owner",
	})
	if err != nil || !ok {
		t.Fatalf("allow = %v err = %v, want true", ok, err)
	}
}

func TestEvaluatorGranteeDenyWrongAction(t *testing.T) {
	t.Parallel()
	eval := &authz.Evaluator{Grants: staticGrantStore{grants: []authz.Grant{{
		BucketName: "b", GranteeUserID: "g", Action: authz.ActionGetObject, KeyPrefix: "", Active: true,
	}}}}
	ok, err := eval.Allow(context.Background(), authz.DecisionRequest{
		Principal:     auth.Principal{UserID: "g", Role: "member"},
		Action:        authz.ActionPutObject,
		Bucket:        "b",
		ObjectKey:     "docs/a.txt",
		BucketOwnerID: "owner",
	})
	if err != nil || ok {
		t.Fatalf("allow = %v err = %v, want false", ok, err)
	}
}

func TestEvaluatorPrefixMatchAndMismatch(t *testing.T) {
	t.Parallel()
	eval := &authz.Evaluator{Grants: staticGrantStore{grants: []authz.Grant{{
		BucketName: "b", GranteeUserID: "g", Action: authz.ActionPutObject, KeyPrefix: "uploads/", Active: true,
	}}}}

	ok, err := eval.Allow(context.Background(), authz.DecisionRequest{
		Principal: auth.Principal{UserID: "g", Role: "member"},
		Action:    authz.ActionPutObject, Bucket: "b", ObjectKey: "uploads/a.txt", BucketOwnerID: "owner",
	})
	if err != nil || !ok {
		t.Fatalf("prefix match allow = %v err = %v", ok, err)
	}

	ok, err = eval.Allow(context.Background(), authz.DecisionRequest{
		Principal: auth.Principal{UserID: "g", Role: "member"},
		Action:    authz.ActionPutObject, Bucket: "b", ObjectKey: "other/a.txt", BucketOwnerID: "owner",
	})
	if err != nil || ok {
		t.Fatalf("prefix mismatch allow = %v err = %v, want false", ok, err)
	}
}

func TestEvaluatorInactiveGrantIgnored(t *testing.T) {
	t.Parallel()
	// Store contract: only active grants returned. Also ensure Active=false is ignored if present.
	eval := &authz.Evaluator{Grants: staticGrantStore{grants: []authz.Grant{{
		BucketName: "b", GranteeUserID: "g", Action: authz.ActionGetObject, KeyPrefix: "", Active: false,
	}}}}
	ok, err := eval.Allow(context.Background(), authz.DecisionRequest{
		Principal: auth.Principal{UserID: "g", Role: "member"},
		Action:    authz.ActionGetObject, Bucket: "b", ObjectKey: "k", BucketOwnerID: "owner",
	})
	if err != nil || ok {
		t.Fatalf("allow = %v err = %v, want false", ok, err)
	}
}

func TestEvaluatorCreateBucketAuthenticatedMember(t *testing.T) {
	t.Parallel()
	eval := &authz.Evaluator{Grants: staticGrantStore{}}
	ok, err := eval.Allow(context.Background(), authz.DecisionRequest{
		Principal: auth.Principal{UserID: "m", Role: "member"},
		Action:    authz.ActionCreateBucket,
	})
	if err != nil || !ok {
		t.Fatalf("allow = %v err = %v, want true", ok, err)
	}
}

func TestEvaluatorDeleteBucketDeniedForGrantee(t *testing.T) {
	t.Parallel()
	eval := &authz.Evaluator{Grants: staticGrantStore{grants: []authz.Grant{
		{BucketName: "b", GranteeUserID: "g", Action: authz.ActionGetObject, Active: true},
		{BucketName: "b", GranteeUserID: "g", Action: authz.ActionPutObject, Active: true},
		{BucketName: "b", GranteeUserID: "g", Action: authz.ActionDeleteObject, Active: true},
		{BucketName: "b", GranteeUserID: "g", Action: authz.ActionListBucket, Active: true},
	}}}
	ok, err := eval.Allow(context.Background(), authz.DecisionRequest{
		Principal: auth.Principal{UserID: "g", Role: "member"},
		Action:    authz.ActionDeleteBucket, Bucket: "b", BucketOwnerID: "owner",
	})
	if err != nil || ok {
		t.Fatalf("allow = %v err = %v, want false", ok, err)
	}
}

func TestEvaluatorListBucketPrefixRules(t *testing.T) {
	t.Parallel()
	eval := &authz.Evaluator{Grants: staticGrantStore{grants: []authz.Grant{{
		BucketName: "b", GranteeUserID: "g", Action: authz.ActionListBucket, KeyPrefix: "docs/", Active: true,
	}}}}

	// Can list under grant prefix.
	ok, err := eval.Allow(context.Background(), authz.DecisionRequest{
		Principal: auth.Principal{UserID: "g", Role: "member"},
		Action:    authz.ActionListBucket, Bucket: "b", ListPrefix: "docs/", BucketOwnerID: "owner",
	})
	if err != nil || !ok {
		t.Fatalf("list covered allow = %v err = %v", ok, err)
	}

	// Cannot list whole bucket with empty request prefix.
	ok, err = eval.Allow(context.Background(), authz.DecisionRequest{
		Principal: auth.Principal{UserID: "g", Role: "member"},
		Action:    authz.ActionListBucket, Bucket: "b", ListPrefix: "", BucketOwnerID: "owner",
	})
	if err != nil || ok {
		t.Fatalf("list empty prefix allow = %v err = %v, want false", ok, err)
	}
}

func TestEvaluatorStoreErrorDoesNotAllow(t *testing.T) {
	t.Parallel()
	eval := &authz.Evaluator{Grants: staticGrantStore{err: errors.New("db down")}}
	ok, err := eval.Allow(context.Background(), authz.DecisionRequest{
		Principal: auth.Principal{UserID: "g", Role: "member"},
		Action:    authz.ActionGetObject, Bucket: "b", ObjectKey: "k", BucketOwnerID: "owner",
	})
	if err == nil || ok {
		t.Fatalf("allow = %v err = %v, want false with error", ok, err)
	}
}

func TestEvaluatorDefaultDenyNoGrants(t *testing.T) {
	t.Parallel()
	eval := &authz.Evaluator{Grants: staticGrantStore{}}
	ok, err := eval.Allow(context.Background(), authz.DecisionRequest{
		Principal: auth.Principal{UserID: "g", Role: "member"},
		Action:    authz.ActionGetObject, Bucket: "b", ObjectKey: "k", BucketOwnerID: "owner",
	})
	if err != nil || ok {
		t.Fatalf("allow = %v err = %v, want false", ok, err)
	}
}

func TestPrefixMatches(t *testing.T) {
	t.Parallel()
	cases := []struct {
		prefix, key string
		want        bool
	}{
		{"", "any", true},
		{"docs/", "docs/", true},
		{"docs/", "docs/a", true},
		{"docs/", "doc", false},
		{"docs/", "other", false},
	}
	for _, tc := range cases {
		if got := authz.PrefixMatches(tc.prefix, tc.key); got != tc.want {
			t.Fatalf("PrefixMatches(%q,%q)=%v want %v", tc.prefix, tc.key, got, tc.want)
		}
	}
}
