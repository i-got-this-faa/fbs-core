package authz_test

import (
	"context"
	"errors"
	"testing"

	"github.com/i-got-this-faa/fbs/internal/auth"
	"github.com/i-got-this-faa/fbs/internal/authz"
	"github.com/i-got-this-faa/fbs/internal/iam"
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
		Action:        iam.ActionGetObject,
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
		Action:        iam.ActionDeleteBucket,
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
		Action:        iam.ActionGetObject,
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
		BucketName: "b", GranteeUserID: "g", Action: iam.ActionGetObject, KeyPrefix: "", Active: true,
	}}}}
	ok, err := eval.Allow(context.Background(), authz.DecisionRequest{
		Principal:     auth.Principal{UserID: "g", Role: "member"},
		Action:        iam.ActionGetObject,
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
		BucketName: "b", GranteeUserID: "g", Action: iam.ActionGetObject, KeyPrefix: "", Active: true,
	}}}}
	ok, err := eval.Allow(context.Background(), authz.DecisionRequest{
		Principal:     auth.Principal{UserID: "g", Role: "member"},
		Action:        iam.ActionPutObject,
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
		BucketName: "b", GranteeUserID: "g", Action: iam.ActionPutObject, KeyPrefix: "uploads/", Active: true,
	}}}}

	ok, err := eval.Allow(context.Background(), authz.DecisionRequest{
		Principal: auth.Principal{UserID: "g", Role: "member"},
		Action:    iam.ActionPutObject, Bucket: "b", ObjectKey: "uploads/a.txt", BucketOwnerID: "owner",
	})
	if err != nil || !ok {
		t.Fatalf("prefix match allow = %v err = %v", ok, err)
	}

	ok, err = eval.Allow(context.Background(), authz.DecisionRequest{
		Principal: auth.Principal{UserID: "g", Role: "member"},
		Action:    iam.ActionPutObject, Bucket: "b", ObjectKey: "other/a.txt", BucketOwnerID: "owner",
	})
	if err != nil || ok {
		t.Fatalf("prefix mismatch allow = %v err = %v, want false", ok, err)
	}
}

func TestEvaluatorInactiveGrantIgnored(t *testing.T) {
	t.Parallel()
	// Store contract: only active grants returned. Also ensure Active=false is ignored if present.
	eval := &authz.Evaluator{Grants: staticGrantStore{grants: []authz.Grant{{
		BucketName: "b", GranteeUserID: "g", Action: iam.ActionGetObject, KeyPrefix: "", Active: false,
	}}}}
	ok, err := eval.Allow(context.Background(), authz.DecisionRequest{
		Principal: auth.Principal{UserID: "g", Role: "member"},
		Action:    iam.ActionGetObject, Bucket: "b", ObjectKey: "k", BucketOwnerID: "owner",
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
		Action:    iam.ActionCreateBucket,
	})
	if err != nil || !ok {
		t.Fatalf("allow = %v err = %v, want true", ok, err)
	}
}

func TestEvaluatorDeleteBucketDeniedForGrantee(t *testing.T) {
	t.Parallel()
	eval := &authz.Evaluator{Grants: staticGrantStore{grants: []authz.Grant{
		{BucketName: "b", GranteeUserID: "g", Action: iam.ActionGetObject, Active: true},
		{BucketName: "b", GranteeUserID: "g", Action: iam.ActionPutObject, Active: true},
		{BucketName: "b", GranteeUserID: "g", Action: iam.ActionDeleteObject, Active: true},
		{BucketName: "b", GranteeUserID: "g", Action: iam.ActionListBucket, Active: true},
	}}}
	ok, err := eval.Allow(context.Background(), authz.DecisionRequest{
		Principal: auth.Principal{UserID: "g", Role: "member"},
		Action:    iam.ActionDeleteBucket, Bucket: "b", BucketOwnerID: "owner",
	})
	if err != nil || ok {
		t.Fatalf("allow = %v err = %v, want false", ok, err)
	}
}

func TestEvaluatorListBucketPrefixRules(t *testing.T) {
	t.Parallel()
	eval := &authz.Evaluator{Grants: staticGrantStore{grants: []authz.Grant{{
		BucketName: "b", GranteeUserID: "g", Action: iam.ActionListBucket, KeyPrefix: "docs/", Active: true,
	}}}}

	// Can list under grant prefix.
	ok, err := eval.Allow(context.Background(), authz.DecisionRequest{
		Principal: auth.Principal{UserID: "g", Role: "member"},
		Action:    iam.ActionListBucket, Bucket: "b", ListPrefix: "docs/", BucketOwnerID: "owner",
	})
	if err != nil || !ok {
		t.Fatalf("list covered allow = %v err = %v", ok, err)
	}

	// Cannot list whole bucket with empty request prefix.
	ok, err = eval.Allow(context.Background(), authz.DecisionRequest{
		Principal: auth.Principal{UserID: "g", Role: "member"},
		Action:    iam.ActionListBucket, Bucket: "b", ListPrefix: "", BucketOwnerID: "owner",
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
		Action:    iam.ActionGetObject, Bucket: "b", ObjectKey: "k", BucketOwnerID: "owner",
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
		Action:    iam.ActionGetObject, Bucket: "b", ObjectKey: "k", BucketOwnerID: "owner",
	})
	if err != nil || ok {
		t.Fatalf("allow = %v err = %v, want false", ok, err)
	}
}
