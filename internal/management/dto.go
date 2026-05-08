package management

import "github.com/i-got-this-faa/fbs/internal/metadata"

type metricsResponse struct {
	BucketCount      int64 `json:"bucket_count"`
	ObjectCount      int64 `json:"object_count"`
	TotalObjectBytes int64 `json:"total_object_bytes"`
	UserCount        int64 `json:"user_count"`
	ActiveUserCount  int64 `json:"active_user_count"`
}

type bucketsResponse struct {
	Buckets []bucketSummaryResponse `json:"buckets"`
}

type bucketSummaryResponse struct {
	Name             string `json:"name"`
	OwnerID          string `json:"owner_id"`
	CreatedAt        string `json:"created_at"`
	ObjectCount      int64  `json:"object_count"`
	TotalObjectBytes int64  `json:"total_object_bytes"`
}

type listObjectsResponse struct {
	Bucket         string                  `json:"bucket"`
	Prefix         string                  `json:"prefix"`
	Delimiter      string                  `json:"delimiter"`
	Limit          int                     `json:"limit"`
	IsTruncated    bool                    `json:"is_truncated"`
	NextCursor     string                  `json:"next_cursor"`
	Objects        []objectSummaryResponse `json:"objects"`
	CommonPrefixes []string                `json:"common_prefixes"`
}

type objectSummaryResponse struct {
	Key         string `json:"key"`
	Size        int64  `json:"size"`
	ETag        string `json:"etag"`
	ContentType string `json:"content_type"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

type objectDetailResponse struct {
	Object objectDetail `json:"object"`
}

type objectDetail struct {
	Key         string `json:"key"`
	Bucket      string `json:"bucket"`
	Size        int64  `json:"size"`
	ETag        string `json:"etag"`
	ContentType string `json:"content_type"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

type keysResponse struct {
	Keys []keyResponse `json:"keys"`
}

type keyResponse struct {
	ID               string `json:"id"`
	DisplayName      string `json:"display_name"`
	AccessKeyID      string `json:"access_key_id"`
	SigV4AccessKeyID string `json:"sigv4_access_key_id"`
	Role             string `json:"role"`
	IsActive         bool   `json:"is_active"`
	CreatedAt        string `json:"created_at"`
	UpdatedAt        string `json:"updated_at"`
}

type createKeyResponse struct {
	Key         keyResponse   `json:"key"`
	BearerToken string        `json:"bearer_token"`
	SigV4       sigv4Response `json:"sigv4"`
}

type sigv4Response struct {
	AccessKeyID string `json:"access_key_id"`
	SecretKey   string `json:"secret_key"`
}

func bucketSummaryDTO(summary metadata.BucketSummary) bucketSummaryResponse {
	return bucketSummaryResponse{
		Name:             summary.Name,
		OwnerID:          summary.OwnerID,
		CreatedAt:        formatTime(summary.CreatedAt),
		ObjectCount:      summary.ObjectCount,
		TotalObjectBytes: summary.TotalObjectBytes,
	}
}

func objectSummaryDTO(obj metadata.Object) objectSummaryResponse {
	return objectSummaryResponse{
		Key:         obj.Key,
		Size:        obj.Size,
		ETag:        obj.ETag,
		ContentType: obj.ContentType,
		CreatedAt:   formatTime(obj.CreatedAt),
		UpdatedAt:   formatTime(obj.UpdatedAt),
	}
}

func objectDetailDTO(obj metadata.Object) objectDetail {
	return objectDetail{
		Key:         obj.Key,
		Bucket:      obj.BucketName,
		Size:        obj.Size,
		ETag:        obj.ETag,
		ContentType: obj.ContentType,
		CreatedAt:   formatTime(obj.CreatedAt),
		UpdatedAt:   formatTime(obj.UpdatedAt),
	}
}

func keyDTO(user metadata.User) keyResponse {
	return keyResponse{
		ID:               user.ID,
		DisplayName:      user.DisplayName,
		AccessKeyID:      user.AccessKeyID,
		SigV4AccessKeyID: user.SigV4AccessKeyID,
		Role:             user.Role,
		IsActive:         user.IsActive,
		CreatedAt:        formatTime(user.CreatedAt),
		UpdatedAt:        formatTime(user.UpdatedAt),
	}
}
