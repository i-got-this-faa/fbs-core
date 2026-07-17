# S3 Compatibility Checklist

Generated from `compat/s3-tests/results/last-run.log` (2026-07-11 07:10 UTC).

Source: ceph/s3-tests functional suite with `markers.core` filter.

## Summary

| Status | Count |
|--------|------:|
| Passed | 150 |
| Failed | 291 |
| Errors | 2 |
| Selected | 443 |
| Pass rate | 33.9% |

## Feature areas

| Feature | Pass | Fail | Error | Rate |
|---------|-----:|-----:|------:|-----:|
| Bucket Ops | 13 | 5 | 0 | 72% |
| Put / Delete Object | 6 | 10 | 0 | 38% |
| Get / Head / Range | 11 | 12 | 0 | 48% |
| List Objects | 64 | 19 | 0 | 77% |
| Multipart Upload | 3 | 38 | 0 | 7% |
| Copy Object | 2 | 2 | 0 | 50% |
| Checksums | 0 | 4 | 0 | 0% |
| Headers / Auth edge cases | 17 | 10 | 0 | 63% |
| Conditional Requests | 0 | 2 | 0 | 0% |
| ACL / Public Access | 4 | 37 | 0 | 10% |
| Bucket Policy | 0 | 7 | 0 | 0% |
| Versioning | 0 | 32 | 0 | 0% |
| Object Lock / WORM | 1 | 36 | 0 | 3% |
| IAM / STS / OIDC | 0 | 0 | 2 | 0% |
| Utils / Misc | 1 | 0 | 0 | 100% |
| Other / Uncategorized | 28 | 77 | 0 | 27% |

Legend: `[x]` passed · `[ ]` failed · `[!]` error

## Bucket Ops (13/18 passed)

- [x] test_bucket_create_delete
- [x] test_bucket_create_naming_bad_short_one
- [x] test_bucket_create_naming_bad_short_two
- [x] test_bucket_create_naming_good_contains_hyphen
- [x] test_bucket_create_naming_good_contains_period
- [x] test_bucket_create_naming_good_long_63
- [x] test_bucket_create_naming_good_starts_alpha
- [x] test_bucket_create_naming_good_starts_digit
- [x] test_bucket_delete_nonempty
- [x] test_bucket_delete_notexist
- [x] test_bucket_head
- [x] test_bucket_head_notexist
- [x] test_bucket_notexist
- [ ] test_bucket_create_exists
- [ ] test_bucket_create_exists_nonowner
- [ ] test_bucket_create_special_key_names
- [ ] test_bucket_get_location
- [ ] test_bucket_recreate_not_overriding

## Put / Delete Object (6/16 passed)

- [x] test_multi_object_delete
- [x] test_multi_object_delete_key_limit
- [x] test_object_write_check_etag
- [x] test_object_write_file
- [x] test_object_write_read_update_read_delete
- [x] test_object_write_to_nonexist_bucket
- [ ] test_cors_presigned_put_object
- [ ] test_cors_presigned_put_object_tenant
- [ ] test_cors_presigned_put_object_tenant_v2
- [ ] test_cors_presigned_put_object_v2
- [ ] test_object_delete_key_bucket_gone
- [ ] test_object_write_cache_control
- [ ] test_object_write_expires
- [ ] test_put_object_current_if_match
- [ ] test_put_object_if_match
- [ ] test_put_object_ifmatch_failed

## Get / Head / Range (11/23 passed)

- [x] test_get_object_ifmatch_good
- [x] test_get_object_ifmodifiedsince_failed
- [x] test_get_object_ifmodifiedsince_good
- [x] test_get_object_ifnonematch_failed
- [x] test_get_object_ifnonematch_good
- [x] test_get_object_ifunmodifiedsince_failed
- [x] test_object_read_not_exist
- [x] test_ranged_big_request_response_code
- [x] test_ranged_request_response_code
- [x] test_ranged_request_return_trailing_bytes_response_code
- [x] test_ranged_request_skip_leading_bytes_response_code
- [ ] test_cors_presigned_get_object
- [ ] test_cors_presigned_get_object_tenant
- [ ] test_cors_presigned_get_object_tenant_v2
- [ ] test_cors_presigned_get_object_v2
- [ ] test_get_object_attributes
- [ ] test_get_object_ifmatch_failed
- [ ] test_get_object_ifunmodifiedsince_good
- [ ] test_get_object_torrent
- [ ] test_object_raw_get_object_gone
- [ ] test_object_read_unreadable
- [ ] test_ranged_request_empty_object
- [ ] test_ranged_request_invalid_range

## List Objects (64/83 passed)

- [x] test_basic_key_count
- [x] test_bucket_list_delimiter_alt
- [x] test_bucket_list_delimiter_basic
- [x] test_bucket_list_delimiter_dot
- [x] test_bucket_list_delimiter_empty
- [x] test_bucket_list_delimiter_none
- [x] test_bucket_list_delimiter_not_exist
- [x] test_bucket_list_delimiter_percentage
- [x] test_bucket_list_delimiter_unreadable
- [x] test_bucket_list_delimiter_whitespace
- [x] test_bucket_list_distinct
- [x] test_bucket_list_empty
- [x] test_bucket_list_many
- [x] test_bucket_list_marker_after_list
- [x] test_bucket_list_marker_empty
- [x] test_bucket_list_marker_none
- [x] test_bucket_list_marker_not_in_list
- [x] test_bucket_list_marker_unreadable
- [x] test_bucket_list_maxkeys_none
- [x] test_bucket_list_maxkeys_one
- [x] test_bucket_list_maxkeys_zero
- [x] test_bucket_list_prefix_alt
- [x] test_bucket_list_prefix_delimiter_alt
- [x] test_bucket_list_prefix_delimiter_delimiter_not_exist
- [x] test_bucket_list_prefix_delimiter_prefix_delimiter_not_exist
- [x] test_bucket_list_prefix_delimiter_prefix_not_exist
- [x] test_bucket_list_prefix_empty
- [x] test_bucket_list_prefix_none
- [x] test_bucket_list_prefix_not_exist
- [x] test_bucket_list_special_prefix
- [x] test_bucket_listv2_both_continuationtoken_startafter
- [x] test_bucket_listv2_delimiter_alt
- [x] test_bucket_listv2_delimiter_basic
- [x] test_bucket_listv2_delimiter_dot
- [x] test_bucket_listv2_delimiter_empty
- [x] test_bucket_listv2_delimiter_none
- [x] test_bucket_listv2_delimiter_not_exist
- [x] test_bucket_listv2_delimiter_percentage
- [x] test_bucket_listv2_delimiter_prefix
- [x] test_bucket_listv2_delimiter_prefix_ends_with_delimiter
- [x] test_bucket_listv2_delimiter_prefix_underscore
- [x] test_bucket_listv2_delimiter_unreadable
- [x] test_bucket_listv2_delimiter_whitespace
- [x] test_bucket_listv2_fetchowner_defaultempty
- [x] test_bucket_listv2_fetchowner_empty
- [x] test_bucket_listv2_many
- [x] test_bucket_listv2_maxkeys_none
- [x] test_bucket_listv2_maxkeys_one
- [x] test_bucket_listv2_maxkeys_zero
- [x] test_bucket_listv2_prefix_alt
- [x] test_bucket_listv2_prefix_basic
- [x] test_bucket_listv2_prefix_delimiter_alt
- [x] test_bucket_listv2_prefix_delimiter_basic
- [x] test_bucket_listv2_prefix_delimiter_delimiter_not_exist
- [x] test_bucket_listv2_prefix_delimiter_prefix_delimiter_not_exist
- [x] test_bucket_listv2_prefix_delimiter_prefix_not_exist
- [x] test_bucket_listv2_prefix_empty
- [x] test_bucket_listv2_prefix_none
- [x] test_bucket_listv2_prefix_not_exist
- [x] test_bucket_listv2_prefix_unreadable
- [x] test_bucket_listv2_startafter_after_list
- [x] test_bucket_listv2_startafter_not_in_list
- [x] test_bucket_listv2_startafter_unreadable
- [x] test_object_write_with_chunked_transfer_encoding
- [ ] test_bucket_list_delimiter_not_skip_special
- [ ] test_bucket_list_delimiter_prefix
- [ ] test_bucket_list_delimiter_prefix_ends_with_delimiter
- [ ] test_bucket_list_delimiter_prefix_underscore
- [ ] test_bucket_list_encoding_basic
- [ ] test_bucket_list_maxkeys_invalid
- [ ] test_bucket_list_objects_anonymous
- [ ] test_bucket_list_objects_anonymous_fail
- [ ] test_bucket_list_prefix_basic
- [ ] test_bucket_list_prefix_delimiter_basic
- [ ] test_bucket_list_prefix_unreadable
- [ ] test_bucket_list_return_data
- [ ] test_bucket_listv2_continuationtoken
- [ ] test_bucket_listv2_continuationtoken_empty
- [ ] test_bucket_listv2_encoding_basic
- [ ] test_bucket_listv2_fetchowner_notempty
- [ ] test_bucket_listv2_objects_anonymous
- [ ] test_bucket_listv2_objects_anonymous_fail
- [ ] test_object_content_encoding_aws_chunked

## Multipart Upload (3/41 passed)

- [x] test_abort_multipart_upload
- [x] test_abort_multipart_upload_not_found
- [x] test_atomic_multipart_upload_write
- [ ] test_get_multipart_checksum_object_attributes
- [ ] test_get_multipart_object_attributes
- [ ] test_get_paginated_multipart_object_attributes
- [ ] test_get_single_multipart_object_attributes
- [ ] test_list_multipart_upload
- [ ] test_list_multipart_upload_owner
- [ ] test_multipart_checksum_sha256
- [ ] test_multipart_copy_improper_range
- [ ] test_multipart_copy_invalid_range
- [ ] test_multipart_copy_multiple_sizes
- [ ] test_multipart_copy_small
- [ ] test_multipart_copy_special_names
- [ ] test_multipart_copy_without_range
- [ ] test_multipart_get_part
- [ ] test_multipart_put_current_object_if_match
- [ ] test_multipart_put_current_object_if_none_match
- [ ] test_multipart_put_object_if_match
- [ ] test_multipart_resend_first_finishes_last
- [ ] test_multipart_reupload_checksum_and_etag
- [ ] test_multipart_single_get_part
- [ ] test_multipart_upload
- [ ] test_multipart_upload_complete_without_create
- [ ] test_multipart_upload_contents
- [ ] test_multipart_upload_empty
- [ ] test_multipart_upload_incorrect_etag
- [ ] test_multipart_upload_missing_part
- [ ] test_multipart_upload_multiple_sizes
- [ ] test_multipart_upload_overwrite_existing_object
- [ ] test_multipart_upload_resend_part
- [ ] test_multipart_upload_size_too_small
- [ ] test_multipart_upload_small
- [ ] test_multipart_use_cksum_helper_crc32
- [ ] test_multipart_use_cksum_helper_crc32c
- [ ] test_multipart_use_cksum_helper_crc64nvme
- [ ] test_multipart_use_cksum_helper_sha1
- [ ] test_multipart_use_cksum_helper_sha256
- [ ] test_non_multipart_get_part
- [ ] test_upload_part_copy_percent_encoded_key

## Copy Object (2/4 passed)

- [x] test_copy_object_ifmatch_good
- [x] test_copy_object_ifnonematch_failed
- [ ] test_copy_object_ifmatch_failed
- [ ] test_copy_object_ifnonematch_good

## Checksums (0/4 passed)

- [ ] test_get_checksum_object_attributes
- [ ] test_object_checksum_crc64nvme
- [ ] test_object_checksum_sha256
- [ ] test_post_object_upload_checksum

## Headers / Auth edge cases (17/27 passed)

- [x] test_bucket_create_bad_contentlength_empty
- [x] test_bucket_create_bad_contentlength_negative
- [x] test_bucket_create_bad_contentlength_none
- [x] test_bucket_create_bad_expect_empty
- [x] test_bucket_create_contentlength_none
- [x] test_object_create_amz_date_and_no_date
- [x] test_object_create_bad_contentlength_empty
- [x] test_object_create_bad_contentlength_negative
- [x] test_object_create_bad_contenttype_empty
- [x] test_object_create_bad_contenttype_invalid
- [x] test_object_create_bad_contenttype_none
- [x] test_object_create_bad_expect_empty
- [x] test_object_create_bad_expect_none
- [x] test_object_create_bad_md5_bad
- [x] test_object_create_bad_md5_invalid_short
- [x] test_object_create_bad_md5_none
- [x] test_object_create_date_and_amz_date
- [ ] test_bucket_create_bad_authorization_empty
- [ ] test_bucket_create_bad_authorization_none
- [ ] test_bucket_create_bad_expect_mismatch
- [ ] test_bucket_put_bad_canned_acl
- [ ] test_object_acl_create_contentlength_none
- [ ] test_object_create_bad_authorization_empty
- [ ] test_object_create_bad_authorization_none
- [ ] test_object_create_bad_contentlength_none
- [ ] test_object_create_bad_expect_mismatch
- [ ] test_object_create_bad_md5_empty

## Conditional Requests (0/2 passed)

- [ ] test_put_current_object_if_match
- [ ] test_put_current_object_if_none_match

## ACL / Public Access (4/41 passed)

- [x] test_object_presigned_put_object_with_acl
- [x] test_object_presigned_put_object_with_acl_tenant
- [x] test_object_raw_authenticated_bucket_acl
- [x] test_object_raw_authenticated_object_acl
- [ ] test_block_public_object_canned_acls
- [ ] test_block_public_put_bucket_acls
- [ ] test_bucket_acl_canned
- [ ] test_bucket_acl_canned_authenticatedread
- [ ] test_bucket_acl_canned_private_to_private
- [ ] test_bucket_acl_canned_publicreadwrite
- [ ] test_bucket_acl_default
- [ ] test_bucket_acl_grant_email_not_exist
- [ ] test_bucket_acl_grant_nonexist_user
- [ ] test_bucket_acl_revoke_all
- [ ] test_bucket_concurrent_set_canned_acl
- [ ] test_bucket_recreate_new_acl
- [ ] test_bucket_recreate_overwrite_acl
- [ ] test_cors_presigned_put_object_tenant_with_acl
- [ ] test_cors_presigned_put_object_with_acl
- [ ] test_get_authpublic_acl_bucket_policy_status
- [ ] test_get_nonpublicpolicy_acl_bucket_policy_status
- [ ] test_get_public_acl_bucket_policy_status
- [ ] test_get_public_block_deny_bucket_policy
- [ ] test_get_publicpolicy_acl_bucket_policy_status
- [ ] test_get_undefined_public_block
- [ ] test_ignore_public_acls
- [ ] test_object_acl_canned
- [ ] test_object_acl_canned_authenticatedread
- [ ] test_object_acl_canned_bucketownerfullcontrol
- [ ] test_object_acl_canned_bucketownerread
- [ ] test_object_acl_canned_during_create
- [ ] test_object_acl_canned_publicreadwrite
- [ ] test_object_acl_default
- [ ] test_object_acl_full_control_verify_attributes
- [ ] test_object_copy_canned_acl
- [ ] test_object_put_acl_mtime
- [ ] test_object_raw_get_bucket_acl
- [ ] test_object_raw_get_object_acl
- [ ] test_put_bucket_acl_grant_group_read
- [ ] test_put_get_delete_public_block
- [ ] test_put_public_block

## Bucket Policy (0/7 passed)

- [ ] test_block_public_policy
- [ ] test_block_public_policy_with_principal
- [ ] test_bucket_policy_allow_notprincipal
- [ ] test_get_bucket_policy_status
- [ ] test_get_nonpublicpolicy_principal_bucket_policy_status
- [ ] test_head_object_404_with_policy_prefix
- [ ] test_multipart_upload_on_a_bucket_with_policy

## Versioning (0/32 passed)

- [ ] test_bucket_list_return_data_versioning
- [ ] test_get_versioned_object_attributes
- [ ] test_multipart_copy_versioned
- [ ] test_object_copy_versioned_bucket
- [ ] test_object_copy_versioned_url_encoding
- [ ] test_object_copy_versioning_multipart_upload
- [ ] test_object_lock_put_obj_retention_versionid
- [ ] test_object_lock_suspend_versioning
- [ ] test_versioned_concurrent_object_create_and_remove
- [ ] test_versioned_concurrent_object_create_concurrent_remove
- [ ] test_versioned_object_acl
- [ ] test_versioned_object_acl_no_version_specified
- [ ] test_versioning_bucket_atomic_upload_return_version_id
- [ ] test_versioning_bucket_create_suspend
- [ ] test_versioning_bucket_multipart_upload_return_version_id
- [ ] test_versioning_concurrent_multi_object_delete
- [ ] test_versioning_copy_obj_version
- [ ] test_versioning_multi_object_delete
- [ ] test_versioning_multi_object_delete_with_marker
- [ ] test_versioning_multi_object_delete_with_marker_create
- [ ] test_versioning_obj_create_overwrite_multipart
- [ ] test_versioning_obj_create_read_remove
- [ ] test_versioning_obj_create_read_remove_head
- [ ] test_versioning_obj_create_versions_remove_all
- [ ] test_versioning_obj_create_versions_remove_special_names
- [ ] test_versioning_obj_list_marker
- [ ] test_versioning_obj_plain_null_version_overwrite
- [ ] test_versioning_obj_plain_null_version_overwrite_suspended
- [ ] test_versioning_obj_plain_null_version_removal
- [ ] test_versioning_obj_suspend_versions
- [ ] test_versioning_obj_suspended_copy
- [ ] test_versioning_stack_delete_merkers

## Object Lock / WORM (1/37 passed)

- [x] test_object_lock_changing_mode_from_governance_with_bypass
- [ ] test_object_lock_changing_mode_from_compliance
- [ ] test_object_lock_changing_mode_from_governance_without_bypass
- [ ] test_object_lock_delete_multipart_object_with_legal_hold_on
- [ ] test_object_lock_delete_multipart_object_with_retention
- [ ] test_object_lock_delete_object_with_legal_hold_off
- [ ] test_object_lock_delete_object_with_legal_hold_on
- [ ] test_object_lock_delete_object_with_retention
- [ ] test_object_lock_delete_object_with_retention_and_marker
- [ ] test_object_lock_get_legal_hold
- [ ] test_object_lock_get_legal_hold_invalid_bucket
- [ ] test_object_lock_get_obj_lock
- [ ] test_object_lock_get_obj_lock_invalid_bucket
- [ ] test_object_lock_get_obj_metadata
- [ ] test_object_lock_get_obj_retention
- [ ] test_object_lock_get_obj_retention_invalid_bucket
- [ ] test_object_lock_get_obj_retention_iso8601
- [ ] test_object_lock_multi_delete_object_with_retention
- [ ] test_object_lock_put_legal_hold
- [ ] test_object_lock_put_legal_hold_invalid_bucket
- [ ] test_object_lock_put_legal_hold_invalid_status
- [ ] test_object_lock_put_obj_lock
- [ ] test_object_lock_put_obj_lock_enable_after_create
- [ ] test_object_lock_put_obj_lock_invalid_bucket
- [ ] test_object_lock_put_obj_lock_invalid_days
- [ ] test_object_lock_put_obj_lock_invalid_mode
- [ ] test_object_lock_put_obj_lock_invalid_status
- [ ] test_object_lock_put_obj_lock_invalid_years
- [ ] test_object_lock_put_obj_lock_with_days_and_years
- [ ] test_object_lock_put_obj_retention
- [ ] test_object_lock_put_obj_retention_increase_period
- [ ] test_object_lock_put_obj_retention_invalid_bucket
- [ ] test_object_lock_put_obj_retention_invalid_mode
- [ ] test_object_lock_put_obj_retention_override_default_retention
- [ ] test_object_lock_put_obj_retention_shorten_period
- [ ] test_object_lock_put_obj_retention_shorten_period_bypass
- [ ] test_object_lock_uploading_obj

## IAM / STS / OIDC (0/2 passed)

- [!] test_verify_add_existing_client_id_to_oidc
- [!] test_verify_update_thumbprintlist_of_oidc

## Utils / Misc (1/1 passed)

- [x] test_generate

## Other / Uncategorized (28/105 passed)

- [x] test_atomic_dual_write_1mb
- [x] test_atomic_dual_write_4mb
- [x] test_atomic_dual_write_8mb
- [x] test_atomic_read_1mb
- [x] test_atomic_read_4mb
- [x] test_atomic_read_8mb
- [x] test_atomic_write_1mb
- [x] test_atomic_write_4mb
- [x] test_atomic_write_8mb
- [x] test_buckets_create_then_list
- [x] test_buckets_list_ctime
- [x] test_bucketv2_notexist
- [x] test_multi_objectv2_delete
- [x] test_multi_objectv2_delete_key_limit
- [x] test_object_copy_16m
- [x] test_object_copy_bucket_not_found
- [x] test_object_copy_diff_bucket
- [x] test_object_copy_key_not_found
- [x] test_object_copy_not_owned_bucket
- [x] test_object_copy_same_bucket
- [x] test_object_copy_verify_contenttype
- [x] test_object_copy_zero_size
- [x] test_object_head_zero_bytes
- [x] test_object_metadata_replaced_on_put
- [x] test_object_put_authenticated
- [x] test_object_raw_authenticated
- [x] test_object_raw_authenticated_bucket_gone
- [x] test_object_raw_authenticated_object_gone
- [ ] test_100_continue
- [ ] test_100_continue_error_retry
- [ ] test_access_bucket_private_object_private
- [ ] test_access_bucket_private_object_publicread
- [ ] test_access_bucket_private_object_publicreadwrite
- [ ] test_access_bucket_private_objectv2_private
- [ ] test_access_bucket_private_objectv2_publicread
- [ ] test_access_bucket_private_objectv2_publicreadwrite
- [ ] test_access_bucket_publicread_object_private
- [ ] test_access_bucket_publicread_object_publicread
- [ ] test_access_bucket_publicread_object_publicreadwrite
- [ ] test_access_bucket_publicreadwrite_object_private
- [ ] test_access_bucket_publicreadwrite_object_publicread
- [ ] test_access_bucket_publicreadwrite_object_publicreadwrite
- [ ] test_block_public_restrict_public_buckets
- [ ] test_cors_header_option
- [ ] test_cors_origin_response
- [ ] test_cors_origin_wildcard
- [ ] test_expected_bucket_owner
- [ ] test_list_buckets_bad_auth
- [ ] test_list_buckets_invalid_auth
- [ ] test_list_buckets_paginated
- [ ] test_object_anon_put
- [ ] test_object_anon_put_write_access
- [ ] test_object_copy_not_owned_object_bucket
- [ ] test_object_copy_replacing_metadata
- [ ] test_object_copy_retaining_metadata
- [ ] test_object_copy_to_itself
- [ ] test_object_copy_to_itself_with_metadata
- [ ] test_object_raw_get
- [ ] test_object_raw_get_bucket_gone
- [ ] test_object_raw_get_x_amz_expires_not_expired
- [ ] test_object_raw_get_x_amz_expires_not_expired_tenant
- [ ] test_object_raw_get_x_amz_expires_out_max_range
- [ ] test_object_raw_get_x_amz_expires_out_positive_range
- [ ] test_object_raw_get_x_amz_expires_out_range_zero
- [ ] test_object_raw_put_authenticated_expired
- [ ] test_object_raw_response_headers
- [ ] test_object_requestid_matches_header_on_error
- [ ] test_object_set_get_metadata_none_to_empty
- [ ] test_object_set_get_metadata_none_to_good
- [ ] test_object_set_get_metadata_overwrite_to_empty
- [ ] test_object_set_get_unicode_metadata
- [ ] test_post_object_anonymous_request
- [ ] test_post_object_authenticated_no_content_type
- [ ] test_post_object_authenticated_request
- [ ] test_post_object_authenticated_request_bad_access_key
- [ ] test_post_object_case_insensitive_condition_fields
- [ ] test_post_object_condition_is_case_sensitive
- [ ] test_post_object_empty_conditions
- [ ] test_post_object_escaped_field_values
- [ ] test_post_object_expired_policy
- [ ] test_post_object_expires_is_case_sensitive
- [ ] test_post_object_ignored_header
- [ ] test_post_object_invalid_access_key
- [ ] test_post_object_invalid_content_length_argument
- [ ] test_post_object_invalid_date_format
- [ ] test_post_object_invalid_request_field_value
- [ ] test_post_object_invalid_signature
- [ ] test_post_object_missing_conditions_list
- [ ] test_post_object_missing_content_length_argument
- [ ] test_post_object_missing_expires_condition
- [ ] test_post_object_missing_policy_condition
- [ ] test_post_object_missing_signature
- [ ] test_post_object_no_key_specified
- [ ] test_post_object_request_missing_policy_specified_field
- [ ] test_post_object_set_invalid_success_code
- [ ] test_post_object_set_key_from_filename
- [ ] test_post_object_set_success_code
- [ ] test_post_object_success_redirect_action
- [ ] test_post_object_upload_larger_than_chunk
- [ ] test_post_object_upload_size_below_minimum
- [ ] test_post_object_upload_size_limit_exceeded
- [ ] test_post_object_upload_size_rgw_chunk_size_bug
- [ ] test_post_object_user_specified_header
- [ ] test_post_object_wrong_bucket
- [ ] test_set_cors

